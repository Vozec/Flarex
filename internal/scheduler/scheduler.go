package scheduler

import (
	"github.com/Vozec/flarex/internal/pool"
)

type Scheduler interface {
	Next() (*pool.Worker, error)
}

type roundRobin struct{ p *pool.Pool }

func NewRoundRobin(p *pool.Pool) Scheduler { return &roundRobin{p} }

func (r *roundRobin) Next() (*pool.Worker, error) { return r.p.NextRR() }

type leastInflight struct{ p *pool.Pool }

func NewLeastInflight(p *pool.Pool) Scheduler { return &leastInflight{p} }

func (l *leastInflight) Next() (*pool.Worker, error) {
	ws := l.p.All()
	if len(ws) == 0 {
		return l.p.NextRR()
	}
	best := ws[0]
	bestN := best.Inflight.Load()
	for _, w := range ws[1:] {
		n := w.Inflight.Load()
		if n < bestN {
			best = w
			bestN = n
		}
	}
	return best, nil
}
