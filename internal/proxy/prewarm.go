package proxy

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/pool"
)

var (
	PrewarmRetries        = 5
	PrewarmAttemptTimeout = 3 * time.Second
	KeepAliveInterval     = 20 * time.Second
)

// KeepAlive keeps the HTTP/2 conn pool warm for every worker. Each pass fires
// a cheap GET /__health through w.HTTPClient() → pins the TLS conn so client
// WS dials reuse the existing stream pool. Targets zero TCP+TLS overhead at
// dial time. Runs until ctx is done.
func KeepAlive(ctx context.Context, p *pool.Pool) {
	t := time.NewTicker(KeepAliveInterval)
	defer t.Stop()
	ping := func() {
		for _, w := range p.All() {
			w := w
			go func() {
				client := w.HTTPClient()
				if client == nil {
					client = wsHTTPClient
				}
				cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				req, _ := http.NewRequestWithContext(cctx, http.MethodGet, w.URL+"/__health", nil)
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ping()
		}
	}
}

func Prewarm(ctx context.Context, p *pool.Pool) {
	t0 := time.Now()
	var wg sync.WaitGroup
	for _, w := range p.All() {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ok bool
			for attempt := 0; attempt < PrewarmRetries; attempt++ {
				cctx, cancel := context.WithTimeout(ctx, PrewarmAttemptTimeout)
				req, _ := http.NewRequestWithContext(cctx, http.MethodGet, w.URL+"/__health", nil)
				resp, err := wsHTTPClient.Do(req)
				cancel()
				if err == nil {
					resp.Body.Close()
					ok = true
					break
				}
				logger.L.Debug().Str("worker", w.Name).Int("attempt", attempt+1).Err(err).Msg("prewarm retry")
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
				}
			}
			w.Healthy.Store(ok)
			if !ok {
				logger.L.Warn().Str("worker", w.Name).Msg("prewarm failed after retries")
			}
		}()
	}
	wg.Wait()
	logger.L.Info().Dur("elapsed", time.Since(t0)).Int("workers", p.Size()).Msg("pool pre-warmed")
}
