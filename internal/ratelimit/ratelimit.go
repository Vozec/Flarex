package ratelimit

import (
	"context"

	"github.com/puzpuzpuz/xsync/v3"
	"golang.org/x/time/rate"
)

type PerHost struct {
	limiters *xsync.MapOf[string, *rate.Limiter]
	qps      float64
	burst    int
}

func NewPerHost(qps float64, burst int) *PerHost {
	return &PerHost{
		limiters: xsync.NewMapOf[string, *rate.Limiter](),
		qps:      qps,
		burst:    burst,
	}
}

func (p *PerHost) Wait(ctx context.Context, host string) error {
	if p == nil || p.qps <= 0 {
		return nil
	}
	l, _ := p.limiters.LoadOrCompute(host, func() *rate.Limiter {
		return rate.NewLimiter(rate.Limit(p.qps), p.burst)
	})
	return l.Wait(ctx)
}
