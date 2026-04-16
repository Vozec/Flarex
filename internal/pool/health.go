package pool

import (
	"context"
	"math"
	"net/http"
	"time"
)

const (
	ewmaAlpha       = 0.3
	healthThreshold = 0.5
)

var (
	HealthIntervalD = 30 * time.Second
	HealthTimeoutD  = 5 * time.Second
)

func (w *Worker) RecordResult(err bool) {
	var obs float64
	if err {
		obs = 1
		w.Errors.Add(1)
	}
	for {
		old := w.EWMAErr.Load()
		oldF := math.Float64frombits(old)
		newF := ewmaAlpha*obs + (1-ewmaAlpha)*oldF
		if w.EWMAErr.CompareAndSwap(old, math.Float64bits(newF)) {
			if newF > healthThreshold {
				w.Healthy.Store(false)
			} else {
				w.Healthy.Store(true)
			}
			return
		}
	}
}

func (w *Worker) EWMAErrRate() float64 {
	return math.Float64frombits(w.EWMAErr.Load())
}

type HealthChecker struct {
	Pool     *Pool
	Interval time.Duration
	Timeout  time.Duration
	Client   *http.Client
}

func NewHealthChecker(p *Pool) *HealthChecker {
	return &HealthChecker{
		Pool:     p,
		Interval: HealthIntervalD,
		Timeout:  HealthTimeoutD,
		Client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     60 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
	}
}

func (h *HealthChecker) Run(ctx context.Context) {
	t := time.NewTicker(h.Interval)
	defer t.Stop()
	h.checkAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.checkAll(ctx)
		}
	}
}

func (h *HealthChecker) checkAll(ctx context.Context) {
	for _, w := range h.Pool.All() {
		w := w
		go h.checkOne(ctx, w)
	}
}

func (h *HealthChecker) checkOne(ctx context.Context, w *Worker) {
	cctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, w.URL+"/__health", nil)
	resp, err := h.Client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		w.Healthy.Store(false)
		return
	}
	if colo := resp.Header.Get("X-CF-Colo"); colo != "" {
		w.SetColo(colo)
	}
	resp.Body.Close()
	w.Healthy.Store(true)
}
