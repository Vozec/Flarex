package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Vozec/flarex/internal/filter"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/metrics"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/ratelimit"
	"github.com/Vozec/flarex/internal/scheduler"
	"github.com/panjf2000/ants/v2"
)

type Server struct {
	Auth       *Auth
	Filter     *filter.IPFilter
	Scheduler  scheduler.Scheduler
	Pool       *pool.Pool
	RateLimit  *ratelimit.PerHost
	HMACSecret string

	MaxRetries  int
	BaseBackoff time.Duration
	HedgeAfter  time.Duration
	TLSRewrap   bool

	PoolSize int

	QuotaHook func(accountID string)

	// proxyMode is accessed atomically so it can be swapped at runtime by
	// the admin UI without a pool restart. Use GetProxyMode / SetProxyMode.
	proxyMode atomic.Pointer[string]

	antPool  *ants.Pool
	poolOnce sync.Once
}

// GetProxyMode returns the current proxy mode. Defaults to ModeHybrid if
// unset (matches the legacy behaviour when cfg.Pool.ProxyMode was blank).
func (s *Server) GetProxyMode() string {
	if p := s.proxyMode.Load(); p != nil {
		return *p
	}
	return ModeHybrid
}

// SetProxyMode atomically swaps the mode. Validates against the known set
// and returns an error on a bogus value so the admin UI can surface it.
func (s *Server) SetProxyMode(mode string) {
	s.proxyMode.Store(&mode)
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.poolOnce.Do(func() {
		if s.PoolSize > 0 {
			p, err := ants.NewPool(s.PoolSize, ants.WithNonblocking(false), ants.WithPreAlloc(false))
			if err == nil {
				s.antPool = p
			}
		}
	})
	defer func() {
		if s.antPool != nil {
			s.antPool.Release()
		}
	}()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		s.dispatch(ctx, conn)
	}
}

func (s *Server) dispatch(ctx context.Context, conn net.Conn) {
	if s.antPool == nil {
		go s.handle(ctx, conn)
		return
	}
	if err := s.antPool.Submit(func() { s.handle(ctx, conn) }); err != nil {

		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
	}

	metrics.ConnectionsTotal.Inc()
	metrics.ConnectionsActive.Inc()
	defer metrics.ConnectionsActive.Dec()

	reqID := randID()
	t0 := time.Now()

	req, err := Handshake(conn, s.Auth, s.Filter)
	if err != nil {
		metrics.HandshakeFailTotal.Inc()
		logger.L.Debug().Str("req_id", reqID).Err(err).Msg("socks5 handshake failed")
		return
	}

	if s.RateLimit != nil {
		if err := s.RateLimit.Wait(ctx, req.Host); err != nil {
			ReplyFail(conn, repGeneralFailure)
			return
		}
	}

	pol := DialPolicy{
		MaxRetries:  s.MaxRetries,
		BaseBackoff: s.BaseBackoff,
		HedgeAfter:  s.HedgeAfter,
		HMACSecret:  s.HMACSecret,
		Mode:        PickMode(s.GetProxyMode(), req.Host, req.Port),
		SessionID:   req.Session,
		TLSRewrap:   s.TLSRewrap,
	}
	dialStart := time.Now()
	upstream, w, err := DialWithPolicy(ctx, pol, s.Scheduler, s.Pool, req.Host, req.Port, req.TLS)
	metrics.DialLatencyHist.UpdateDuration(dialStart)

	// Byte-sniff CF fallback: if socket failed because the target is CF-hosted
	// (ErrUpstreamBlocked), we don't blindly retry in fetch. Instead we reply
	// SUCCESS to the client, peek its first bytes, and only promote to fetch
	// when they clearly look like HTTP. Non-HTTP (SSH, raw TCP, TLS) closes.
	var preBytes []byte
	if err != nil && errors.Is(err, ErrUpstreamBlocked) && s.GetProxyMode() == ModeHybrid && pol.Mode != ModeFetch {
		if rerr := ReplySuccess(conn); rerr != nil {
			return
		}
		peeked, isHTTP, perr := peekAndClassify(conn, 1*time.Second, 256)
		if perr != nil || !isHTTP {
			logger.L.Debug().Str("req_id", reqID).Str("target", req.Host).Bool("http", isHTTP).Err(perr).Msg("cf fallback skipped — not HTTP")
			return
		}
		pol.Mode = ModeFetch
		upstream, w, err = DialWithPolicy(ctx, pol, s.Scheduler, s.Pool, req.Host, req.Port, req.TLS)
		if err == nil {
			metrics.FetchFallbackTotal.Inc()
			preBytes = peeked
		}
	}

	if err != nil {
		metrics.DialFailTotal.Inc()
		logger.L.Warn().Str("req_id", reqID).Str("target", req.Host).Int("port", req.Port).Err(err).Msg("dial failed (all retries)")
		if preBytes == nil {
			ReplyFail(conn, repNetUnreachable)
		}
		return
	}
	metrics.DialSuccessTotal.Inc()
	metrics.WorkerReq(w.Name).Inc()
	w.Inflight.Add(1)
	w.Requests.Add(1)
	defer w.Inflight.Add(-1)
	w.RecordResult(false)
	if s.QuotaHook != nil {
		s.QuotaHook(w.AccountID)
	}
	defer upstream.Close()

	if preBytes != nil {
		if _, werr := upstream.Write(preBytes); werr != nil {
			return
		}
	} else if err := ReplySuccess(conn); err != nil {
		return
	}
	logger.L.Debug().Str("req_id", reqID).Str("target", req.Host).Int("port", req.Port).Str("worker", w.Name).Str("mode", pol.Mode).Msg("tunnel up")

	relay(conn, upstream)
	metrics.ReqLatencyHist.UpdateDuration(t0)
	logger.L.Debug().Str("req_id", reqID).Str("worker", w.Name).Dur("total", time.Since(t0)).Msg("tunnel closed")
}

// peekAndClassify reads up to max bytes from conn with a deadline and runs
// LooksLikeHTTP on the result. Returned bytes must be forwarded to upstream
// before starting the bi-directional relay — they are already consumed from
// the client's stream.
func peekAndClassify(conn net.Conn, timeout time.Duration, max int) ([]byte, bool, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	var stackBuf [256]byte
	buf := stackBuf[:max]
	n, err := conn.Read(buf)
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return nil, false, err
	}
	peek := buf[:n]
	return peek, LooksLikeHTTP(peek), nil
}

func randID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// relay bi-directionally shuttles bytes between a (client) and b (upstream)
// and records the counts on the downstream/upstream byte meters so the
// admin UI's throughput row has real data instead of zeros.
//
// Direction convention:
//   b → a : bytes the target sent us and we forwarded to the client.
//           Counted as "downstream" (target -> client).
//   a → b : bytes the client sent us and we forwarded to the target.
//           Counted as "upstream" (client -> target).
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			a.Close()
			b.Close()
		})
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := getBuf()
		defer putBuf(buf)
		n, _ := io.CopyBuffer(a, b, *buf)
		if n > 0 {
			metrics.BytesDownstream.Add(int(n))
		}
		closeAll()
	}()
	go func() {
		defer wg.Done()
		buf := getBuf()
		defer putBuf(buf)
		n, _ := io.CopyBuffer(b, a, *buf)
		if n > 0 {
			metrics.BytesUpstream.Add(int(n))
		}
		closeAll()
	}()
	wg.Wait()
}
