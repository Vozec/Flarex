package proxy

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"

	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/metrics"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/scheduler"
	"github.com/Vozec/flarex/internal/tlsdial"
	"github.com/Vozec/flarex/internal/tracing"
	"github.com/sony/gobreaker/v2"
)

type DialPolicy struct {
	MaxRetries  int
	BaseBackoff time.Duration
	HedgeAfter  time.Duration
	HMACSecret  string
	Mode        string // socket | fetch | hybrid
	SessionID   string // optional stickiness key
	TLSRewrap   bool   // if true + tls=true + mode=socket, wrap conn with uTLS using random fingerprint
	// AutoFetchFallback: on ErrUpstreamBlocked (CF-hosted target), silently retry
	// the same worker in fetch mode. Default false: caller (server.go) does a
	// byte-sniff first, gating fetch to confirmed HTTP streams only. Set true to
	// preserve legacy port-based behavior — risks corrupting non-HTTP traffic.
	AutoFetchFallback bool
}

func DialWithPolicy(
	ctx context.Context,
	pol DialPolicy,
	sched scheduler.Scheduler,
	p *pool.Pool,
	host string, port int, tls bool,
) (net.Conn, *pool.Worker, error) {
	ctx, span := tracing.StartDial(ctx, host, port)
	defer span.End()

	if pol.MaxRetries <= 0 {
		pol.MaxRetries = 3
	}
	if pol.BaseBackoff == 0 {
		pol.BaseBackoff = 50 * time.Millisecond
	}

	// Pre-emptive fetch promotion on cached CF-hosted targets is gated behind
	// AutoFetchFallback — it's unsafe without byte-sniffing since fetch can't
	// carry TLS/SSH/raw-TCP. Without the flag, every dial attempts socket first
	// and the server layer sniffs + promotes only HTTP streams.
	if pol.AutoFetchFallback && (pol.Mode == ModeHybrid || pol.Mode == ModeSocket) && IsKnownUnreachable(host, port) {
		pol.Mode = ModeFetch
	}

	tried := make(map[string]struct{}, pol.MaxRetries)
	var lastErr error

	for attempt := 0; attempt < pol.MaxRetries; attempt++ {
		var w *pool.Worker
		var err error
		if attempt == 0 && pol.SessionID != "" {
			w, err = p.NextBySession(pol.SessionID)
		} else if attempt == 0 {
			w, err = sched.Next()
		} else {
			w, err = p.NextSkip(tried)
		}
		if err != nil {
			lastErr = err
			break
		}
		tried[w.Name] = struct{}{}

		conn, derr := dialOnceWithHedge(ctx, pol, p, w, host, port, tls, tried)
		if derr == nil {
			wrapped, werr := maybeRewrap(ctx, pol, conn, host, tls)
			if werr != nil {
				w.RecordResult(true)
				lastErr = werr
				continue
			}
			span.SetAttributes(
				tracing.Str("worker", w.Name),
				tracing.Str("mode", pol.Mode),
				tracing.Int("retry_count", attempt),
			)
			return wrapped, w, nil
		}
		// Transparent CF fallback: on upstream-blocked (close 4001), only auto-retry
		// in fetch mode when AutoFetchFallback is set. Caller should leave this
		// false and sniff client bytes before promoting to fetch — fetch is
		// HTTP-only at the Worker, so blind fallback corrupts non-HTTP streams.
		if errors.Is(derr, ErrUpstreamBlocked) {
			MarkUnreachableViaSocket(host, port)
			if pol.AutoFetchFallback && pol.Mode != ModeFetch {
				logger.L.Info().Str("target", host).Int("port", port).Str("worker", w.Name).Msg("upstream blocked — auto fetch fallback (unchecked)")
				pol.Mode = ModeFetch
				fallbackConn, ferr := dialBreak(ctx, w, pol.HMACSecret, host, port, tls, pol.Mode)
				if ferr == nil {
					wrapped, werr := maybeRewrap(ctx, pol, fallbackConn, host, tls)
					if werr == nil {
						return wrapped, w, nil
					}
					ferr = werr
				}
				logger.L.Warn().Str("target", host).Str("worker", w.Name).Err(ferr).Msg("fetch fallback also failed")
				derr = ferr
			} else {
				// Caller asked for byte-sniff gating. Retrying other workers is
				// pointless — they'll all hit the same CF-blocks-CF rule. Return
				// the sentinel so the caller can sniff + decide.
				return nil, nil, ErrUpstreamBlocked
			}
		}
		if !errors.Is(derr, ErrUpstreamBlocked) {
			w.RecordResult(true)
		}
		lastErr = derr
		logger.L.Debug().Str("worker", w.Name).Int("attempt", attempt+1).Err(derr).Msg("dial fail, retry")

		back := pol.BaseBackoff << attempt
		back += time.Duration(rand.Int64N(int64(pol.BaseBackoff)))
		select {
		case <-time.After(back):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all retries exhausted")
	}
	return nil, nil, lastErr
}

type dialRes struct {
	conn   net.Conn
	err    error
	worker *pool.Worker
}

func dialOnceWithHedge(
	ctx context.Context,
	pol DialPolicy,
	p *pool.Pool,
	primary *pool.Worker,
	host string, port int, tls bool,
	tried map[string]struct{},
) (net.Conn, error) {
	if pol.HedgeAfter <= 0 {
		return dialBreak(ctx, primary, pol.HMACSecret, host, port, tls, pol.Mode)
	}

	resCh := make(chan dialRes, 2)
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		c, e := dialBreak(dctx, primary, pol.HMACSecret, host, port, tls, pol.Mode)
		select {
		case resCh <- dialRes{c, e, primary}:
		case <-dctx.Done():
			if c != nil {
				c.Close()
			}
		}
	}()

	hedgeTimer := time.NewTimer(pol.HedgeAfter)
	defer hedgeTimer.Stop()

	var hedgeFired bool
	for {
		select {
		case <-dctx.Done():
			return nil, dctx.Err()
		case r := <-resCh:
			if r.err == nil {
				if hedgeFired && r.worker != primary {
					metrics.HedgeWinsTotal.Inc()
				}
				cancel()
				go drainResChan(resCh)
				return r.conn, nil
			}

			if !hedgeFired {
				hedgeFired = true
				w2, err := p.NextSkip(tried)
				if err != nil {
					return nil, r.err
				}
				tried[w2.Name] = struct{}{}
				metrics.HedgeFiredTotal.Inc()
				go func() {
					c, e := dialBreak(dctx, w2, pol.HMACSecret, host, port, tls, pol.Mode)
					select {
					case resCh <- dialRes{c, e, w2}:
					case <-dctx.Done():
						if c != nil {
							c.Close()
						}
					}
				}()
				continue
			}
			return nil, r.err
		case <-hedgeTimer.C:
			if hedgeFired {
				continue
			}
			hedgeFired = true
			w2, err := p.NextSkip(tried)
			if err != nil {
				continue
			}
			tried[w2.Name] = struct{}{}
			metrics.HedgeFiredTotal.Inc()
			go func() {
				c, e := dialBreak(dctx, w2, pol.HMACSecret, host, port, tls, pol.Mode)
				select {
				case resCh <- dialRes{c, e, w2}:
				case <-dctx.Done():
					if c != nil {
						c.Close()
					}
				}
			}()
		}
	}
}

// maybeRewrap wraps conn with uTLS when pol.TLSRewrap is enabled and the
// target is TLS. Only meaningful in socket mode — fetch mode delegates TLS
// to the Worker's fetch() call at the CF edge.
func maybeRewrap(ctx context.Context, pol DialPolicy, conn net.Conn, host string, tls bool) (net.Conn, error) {
	if !pol.TLSRewrap || !tls || pol.Mode != ModeSocket {
		return conn, nil
	}
	wrapped, err := tlsdial.WrapRandom(ctx, conn, host)
	if err != nil {
		conn.Close()
		logger.L.Debug().Str("host", host).Err(err).Msg("utls rewrap failed")
		return nil, err
	}
	return wrapped, nil
}

func dialBreak(ctx context.Context, w *pool.Worker, hmacSecret, host string, port int, tls bool, mode string) (net.Conn, error) {
	var conn net.Conn
	_, err := w.Breaker().Execute(func() (struct{}, error) {
		c, e := DialWorker(ctx, w, hmacSecret, host, port, tls, mode)
		if e != nil {
			return struct{}{}, e
		}
		conn = c
		return struct{}{}, nil
	})
	if err != nil {

		if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
			return nil, fmt.Errorf("breaker %s: %w", w.Name, err)
		}
		return nil, err
	}
	return conn, nil
}

func drainResChan(ch <-chan dialRes) {
	// Non-blocking drain of already-buffered results. In-flight
	// goroutines clean up their own connections via <-dctx.Done().
	for {
		select {
		case r := <-ch:
			if r.conn != nil {
				r.conn.Close()
			}
		default:
			return
		}
	}
}
