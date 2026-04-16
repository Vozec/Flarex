package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	neturl "net/url"
	"sync"
	"time"

	"github.com/Vozec/flarex/internal/backend"
	"github.com/Vozec/flarex/internal/config"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/state"
	"golang.org/x/sync/errgroup"
)

func DeployAll(ctx context.Context, cfg *config.Config, st *state.Store) ([]*pool.Worker, error) {
	script, err := Render(cfg.Security.HMACSecret, cfg.Worker.TemplatePath)
	if err != nil {
		return nil, fmt.Errorf("render template: %w", err)
	}

	var mu sync.Mutex
	var workers []*pool.Worker

	g, gctx := errgroup.WithContext(ctx)
	for _, acc := range cfg.Accounts {
		acc := acc
		backends, err := backend.Pick(ctx, backend.Mode(cfg.Worker.DeployBackend), acc.ID, acc.Token, acc.Subdomain)
		if err != nil {
			return workers, fmt.Errorf("backend pick %s: %w", acc.ID, err)
		}
		for _, b := range backends {
			b := b
			// Idempotency: count existing workers on this backend and only
			// deploy the delta so `flarex deploy` run twice is a no-op when
			// the pool already matches worker.count. Per (account, backend).
			existing, err := b.List(gctx, cfg.Worker.NamePrefix)
			if err != nil {
				logger.L.Warn().Str("account", acc.ID).Str("backend", b.Name()).Err(err).Msg("list existing workers (treating as empty)")
				existing = nil
			}
			have := len(existing)
			want := cfg.Worker.Count
			if have >= want {
				logger.L.Info().Str("account", acc.ID).Str("backend", b.Name()).Int("have", have).Int("want", want).Msg("deploy: already at target count, skipping")
				// Still want to backfill state for already-deployed workers.
				for _, d := range existing {
					w := workerFromDeployed(d)
					mu.Lock()
					workers = append(workers, w)
					mu.Unlock()
					if st != nil {
						_ = st.PutWorker(workerRec(d, w.CreatedAt))
					}
				}
				continue
			}
			logger.L.Info().Str("account", acc.ID).Int("have", have).Int("want", want).Msg("deploy: filling pool")
			// Re-add existing to the returned pool so callers see the full set.
			for _, d := range existing {
				w := workerFromDeployed(d)
				mu.Lock()
				workers = append(workers, w)
				mu.Unlock()
				if st != nil {
					_ = st.PutWorker(workerRec(d, w.CreatedAt))
				}
			}
			for i := 0; i < want-have; i++ {
				g.Go(func() error {
					name := cfg.Worker.NamePrefix + randHex(6)
					d, err := b.Deploy(gctx, name, script)
					if err != nil {
						return fmt.Errorf("deploy %s on %s: %w", name, b.Name(), err)
					}
					w := workerFromDeployed(d)
					mu.Lock()
					workers = append(workers, w)
					mu.Unlock()
					if st != nil {
						_ = st.PutWorker(workerRec(d, w.CreatedAt))
					}
					logger.L.Info().Str("name", d.Name).Str("url", d.URL).Str("backend", d.Backend).Msg("worker deployed")
					return nil
				})
			}
		}
	}
	if err := g.Wait(); err != nil {
		return workers, err
	}
	return workers, nil
}

func DestroyAll(ctx context.Context, cfg *config.Config, st *state.Store) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, acc := range cfg.Accounts {
		acc := acc
		g.Go(func() error {
			backends, err := backend.Pick(gctx, backend.Mode(cfg.Worker.DeployBackend), acc.ID, acc.Token, acc.Subdomain)
			if err != nil {
				return err
			}
			for _, b := range backends {
				ds, err := b.List(gctx, cfg.Worker.NamePrefix)
				if err != nil {
					logger.L.Warn().Str("backend", b.Name()).Err(err).Msg("list workers")
					continue
				}
				for _, d := range ds {
					if err := b.Delete(gctx, d); err != nil {
						logger.L.Warn().Str("name", d.Name).Err(err).Msg("delete worker")
						continue
					}
					if st != nil {
						_ = st.DeleteWorker(d.Name)
					}
					logger.L.Info().Str("name", d.Name).Str("backend", d.Backend).Msg("worker deleted")
				}
			}
			return nil
		})
	}
	return g.Wait()
}

func ListAll(ctx context.Context, cfg *config.Config) ([]*pool.Worker, error) {
	var mu sync.Mutex
	var workers []*pool.Worker
	g, gctx := errgroup.WithContext(ctx)
	for _, acc := range cfg.Accounts {
		acc := acc
		g.Go(func() error {
			backends, err := backend.Pick(gctx, backend.Mode(cfg.Worker.DeployBackend), acc.ID, acc.Token, acc.Subdomain)
			if err != nil {
				return err
			}
			for _, b := range backends {
				ds, err := b.List(gctx, cfg.Worker.NamePrefix)
				if err != nil {
					return err
				}
				for _, d := range ds {
					mu.Lock()
					workers = append(workers, workerFromDeployed(d))
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return workers, err
	}
	return workers, nil
}

func DeployOnAccount(ctx context.Context, cfg *config.Config, st *state.Store, acc config.Account, count int) ([]*pool.Worker, error) {
	if count <= 0 {
		count = cfg.Worker.Count
	}
	script, err := Render(cfg.Security.HMACSecret, cfg.Worker.TemplatePath)
	if err != nil {
		return nil, fmt.Errorf("render template: %w", err)
	}
	backends, err := backend.Pick(ctx, backend.Mode(cfg.Worker.DeployBackend), acc.ID, acc.Token, acc.Subdomain)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var workers []*pool.Worker
	g, gctx := errgroup.WithContext(ctx)
	for _, b := range backends {
		b := b
		for i := 0; i < count; i++ {
			g.Go(func() error {
				name := cfg.Worker.NamePrefix + randHex(6)
				d, err := b.Deploy(gctx, name, script)
				if err != nil {
					return err
				}
				w := workerFromDeployed(d)
				mu.Lock()
				workers = append(workers, w)
				mu.Unlock()
				if st != nil {
					_ = st.PutWorker(workerRec(d, w.CreatedAt))
				}
				logger.L.Info().Str("name", d.Name).Str("url", d.URL).Str("backend", d.Backend).Str("account", acc.ID).Msg("worker deployed (runtime)")
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return workers, err
	}
	return workers, nil
}

func LoadFromState(st *state.Store) ([]*pool.Worker, error) {
	recs, err := st.ListWorkers()
	if err != nil {
		return nil, err
	}
	out := make([]*pool.Worker, 0, len(recs))
	for _, r := range recs {
		w := pool.NewWorker(r.AccountID, r.Name, r.URL)
		w.CreatedAt = r.CreatedAt
		w.Backend = r.Backend
		w.Hostname = r.Hostname
		if w.Hostname == "" {

			w.Hostname = hostnameFromURL(r.URL)
		}
		w.ZoneID = r.ZoneID
		w.RecordID = r.RecordID
		out = append(out, w)
	}
	return out, nil
}

func hostnameFromURL(u string) string {
	parsed, err := neturl.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func workerFromDeployed(d *backend.Deployed) *pool.Worker {
	w := pool.NewWorker(d.AccountID, d.Name, d.URL)
	w.Backend = d.Backend
	w.Hostname = d.Hostname
	w.ZoneID = d.ZoneID
	w.RecordID = d.RecordID
	return w
}

func workerRec(d *backend.Deployed, createdAt time.Time) state.WorkerRec {
	return state.WorkerRec{
		Name:      d.Name,
		AccountID: d.AccountID,
		URL:       d.URL,
		CreatedAt: createdAt,
		Backend:   d.Backend,
		Hostname:  d.Hostname,
		ZoneID:    d.ZoneID,
		RecordID:  d.RecordID,
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
