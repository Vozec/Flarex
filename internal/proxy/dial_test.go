package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/scheduler"
)

func TestDialRetryAcrossWorkers(t *testing.T) {

	ws := []*pool.Worker{
		pool.NewWorker("a", "w1", "http://127.0.0.1:1"),
		pool.NewWorker("a", "w2", "http://127.0.0.1:2"),
		pool.NewWorker("a", "w3", "http://127.0.0.1:3"),
	}
	p := pool.New(ws)
	sched := scheduler.NewRoundRobin(p)

	pol := DialPolicy{MaxRetries: 3, BaseBackoff: 1 * time.Millisecond, HMACSecret: "x"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := DialWithPolicy(ctx, pol, sched, p, "example.com", 443, true)
	if err == nil {
		t.Fatal("expected error (all workers down)")
	}

	totalErrors := uint64(0)
	for _, w := range ws {
		totalErrors += w.Errors.Load()
	}
	if totalErrors == 0 {
		t.Error("expected error counters to bump")
	}
}

func TestDialPolicyDefaults(t *testing.T) {
	ws := []*pool.Worker{pool.NewWorker("a", "w1", "http://127.0.0.1:1")}
	p := pool.New(ws)
	sched := scheduler.NewRoundRobin(p)

	pol := DialPolicy{HMACSecret: "x"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := DialWithPolicy(ctx, pol, sched, p, "example.com", 443, true)
	if err == nil {
		t.Fatal("expected error")
	}
}
