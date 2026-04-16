package worker

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Vozec/flarex/internal/backend"
	"github.com/Vozec/flarex/internal/config"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/state"
)

type Rotator struct {
	Cfg      *config.Config
	Pool     *pool.Pool
	State    *state.Store
	Interval time.Duration
	MaxAge   time.Duration
	MaxReq   uint64
}

func (r *Rotator) Run(ctx context.Context) {
	if r.Interval == 0 {
		r.Interval = 5 * time.Minute
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Rotator) tick(ctx context.Context) {
	if r.MaxAge == 0 && r.MaxReq == 0 {
		return
	}
	script, err := Render(r.Cfg.Security.HMACSecret, r.Cfg.Worker.TemplatePath)
	if err != nil {
		logger.L.Warn().Err(err).Msg("rotator: render template")
		return
	}

	for _, w := range r.Pool.All() {
		w := w
		if !shouldRecycle(w, r.MaxAge, r.MaxReq) {
			continue
		}
		logger.L.Info().Str("worker", w.Name).Dur("age", time.Since(w.CreatedAt)).Uint64("reqs", w.Requests.Load()).Msg("rotator: recycling")
		go r.recycle(ctx, w, script)
	}
}

func shouldRecycle(w *pool.Worker, maxAge time.Duration, maxReq uint64) bool {
	if maxAge > 0 && time.Since(w.CreatedAt) > maxAge {
		return true
	}
	if maxReq > 0 && w.Requests.Load() > maxReq {
		return true
	}
	return false
}

var (
	rotateMu      sync.Mutex
	DrainTimeoutD = 30 * time.Second
	DrainPollD    = 100 * time.Millisecond
)

// VerifyTemplates checks every worker's /__health header X-Template-Hash.
// Mismatch → recycle (redeploy drifted workers with current template).
// Runs concurrently; caller should run in background goroutine.
func (r *Rotator) VerifyTemplates(ctx context.Context) {
	expected, err := TemplateHash(r.Cfg.Worker.TemplatePath)
	if err != nil {
		logger.L.Warn().Err(err).Msg("verify: hash template")
		return
	}
	script, err := Render(r.Cfg.Security.HMACSecret, r.Cfg.Worker.TemplatePath)
	if err != nil {
		logger.L.Warn().Err(err).Msg("verify: render template")
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var checked, drifted int
	for _, w := range r.Pool.All() {
		w := w
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.URL+"/__health", nil)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		got := resp.Header.Get("X-Template-Hash")
		resp.Body.Close()
		checked++
		if got == "" || got == expected {
			continue
		}
		drifted++
		logger.L.Info().Str("worker", w.Name).Str("have", got[:12]).Str("want", expected[:12]).Msg("verify: template drift, recycling")
		go r.recycle(ctx, w, script)
	}
	logger.L.Info().Int("checked", checked).Int("drifted", drifted).Msg("verify: template hash check done")
}

func drain(ctx context.Context, w *pool.Worker) {
	deadline := time.Now().Add(DrainTimeoutD)
	for time.Now().Before(deadline) {
		if w.Inflight.Load() == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(DrainPollD):
		}
	}
	logger.L.Warn().Str("worker", w.Name).Int64("inflight", w.Inflight.Load()).Msg("drain: timeout, force delete")
}

func (r *Rotator) recycle(ctx context.Context, old *pool.Worker, script string) {

	var acc *config.Account
	for i := range r.Cfg.Accounts {
		if r.Cfg.Accounts[i].ID == old.AccountID {
			acc = &r.Cfg.Accounts[i]
			break
		}
	}
	if acc == nil {
		return
	}

	backends, err := backend.Pick(ctx, backend.Mode(r.Cfg.Worker.DeployBackend), acc.ID, acc.Token, acc.Subdomain)
	if err != nil || len(backends) == 0 {
		logger.L.Warn().Err(err).Msg("rotator: no backend available")
		return
	}
	var b backend.Backend
	if old.Backend != "" {
		for _, candidate := range backends {
			if candidate.Name() == old.Backend {
				b = candidate
				break
			}
		}
	}
	if b == nil {
		b = backends[0]
	}

	newName := r.Cfg.Worker.NamePrefix + randHex(6)
	d, err := b.Deploy(ctx, newName, script)
	if err != nil {
		logger.L.Warn().Str("new", newName).Err(err).Msg("rotator: deploy failed")
		return
	}

	rotateMu.Lock()
	newW := pool.NewWorker(old.AccountID, newName, d.URL)
	newW.Backend = d.Backend
	newW.Hostname = d.Hostname
	newW.ZoneID = d.ZoneID
	newW.RecordID = d.RecordID
	r.Pool.Replace(old, newW)
	rotateMu.Unlock()

	old.Healthy.Store(false)
	drain(ctx, old)

	for _, candidate := range backends {
		if candidate.Name() == old.Backend {
			oldDeployed := &backend.Deployed{
				Name: old.Name, AccountID: old.AccountID, Backend: old.Backend,
				Hostname: old.Hostname, ZoneID: old.ZoneID, RecordID: old.RecordID,
			}
			if err := candidate.Delete(ctx, oldDeployed); err != nil {
				logger.L.Warn().Str("old", old.Name).Err(err).Msg("rotator: delete old")
			}
			break
		}
	}

	if r.State != nil {
		_ = r.State.DeleteWorker(old.Name)
		_ = r.State.PutWorker(state.WorkerRec{
			Name:      newName,
			AccountID: old.AccountID,
			URL:       d.URL,
			CreatedAt: time.Now(),
			Backend:   d.Backend,
			Hostname:  d.Hostname,
			ZoneID:    d.ZoneID,
			RecordID:  d.RecordID,
		})
	}

	logger.L.Info().Str("old", old.Name).Str("new", newName).Str("backend", d.Backend).Msg("rotator: swap done")
}

// RecyclePublic is an admin-facing entry point that runs the same logic as
// the background rotator would for a single worker on demand. Returns the
// new worker name. Script is rendered if blank.
func (r *Rotator) RecyclePublic(ctx context.Context, old *pool.Worker, script string) (string, error) {
	if script == "" {
		s, err := Render(r.Cfg.Security.HMACSecret, r.Cfg.Worker.TemplatePath)
		if err != nil {
			return "", err
		}
		script = s
	}
	var acc *config.Account
	for i := range r.Cfg.Accounts {
		if r.Cfg.Accounts[i].ID == old.AccountID {
			acc = &r.Cfg.Accounts[i]
			break
		}
	}
	if acc == nil {
		return "", fmt.Errorf("no account matches worker %s (id=%s)", old.Name, old.AccountID)
	}
	backends, err := backend.Pick(ctx, backend.Mode(r.Cfg.Worker.DeployBackend), acc.ID, acc.Token, acc.Subdomain)
	if err != nil || len(backends) == 0 {
		return "", fmt.Errorf("no backend: %w", err)
	}
	var b backend.Backend
	for _, c := range backends {
		if c.Name() == old.Backend {
			b = c
			break
		}
	}
	if b == nil {
		b = backends[0]
	}
	newName := r.Cfg.Worker.NamePrefix + randHex(6)
	d, err := b.Deploy(ctx, newName, script)
	if err != nil {
		return "", fmt.Errorf("deploy: %w", err)
	}
	rotateMu.Lock()
	newW := pool.NewWorker(old.AccountID, newName, d.URL)
	newW.Backend = d.Backend
	newW.Hostname = d.Hostname
	newW.ZoneID = d.ZoneID
	newW.RecordID = d.RecordID
	r.Pool.Replace(old, newW)
	rotateMu.Unlock()
	old.Healthy.Store(false)
	drain(ctx, old)
	for _, c := range backends {
		if c.Name() == old.Backend {
			oldDeployed := &backend.Deployed{
				Name: old.Name, AccountID: old.AccountID, Backend: old.Backend,
				Hostname: old.Hostname, ZoneID: old.ZoneID, RecordID: old.RecordID,
			}
			if err := c.Delete(ctx, oldDeployed); err != nil {
				logger.L.Warn().Str("old", old.Name).Err(err).Msg("recycle: delete old")
			}
			break
		}
	}
	if r.State != nil {
		_ = r.State.DeleteWorker(old.Name)
		_ = r.State.PutWorker(state.WorkerRec{
			Name: newName, AccountID: old.AccountID, URL: d.URL,
			CreatedAt: time.Now(), Backend: d.Backend,
			Hostname: d.Hostname, ZoneID: d.ZoneID, RecordID: d.RecordID,
		})
	}
	logger.L.Info().Str("old", old.Name).Str("new", newName).Msg("recycle: done (admin-triggered)")
	return newName, nil
}
