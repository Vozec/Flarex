package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestNilNoop(t *testing.T) {
	var rl *PerHost
	if err := rl.Wait(context.Background(), "x"); err != nil {
		t.Error("nil PerHost should be noop")
	}
}

func TestZeroQPSNoop(t *testing.T) {
	rl := NewPerHost(0, 10)
	if err := rl.Wait(context.Background(), "x"); err != nil {
		t.Error("qps=0 should be noop")
	}
}

func TestEnforcesQPS(t *testing.T) {
	rl := NewPerHost(10, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	t0 := time.Now()

	for i := 0; i < 3; i++ {
		if err := rl.Wait(ctx, "h1"); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	elapsed := time.Since(t0)
	if elapsed < 150*time.Millisecond {
		t.Errorf("rate limit violated: %v", elapsed)
	}
}

func TestPerHostIndependent(t *testing.T) {
	rl := NewPerHost(1, 1)
	ctx := context.Background()

	t0 := time.Now()
	rl.Wait(ctx, "a")
	rl.Wait(ctx, "b")
	rl.Wait(ctx, "c")
	if time.Since(t0) > 100*time.Millisecond {
		t.Error("different hosts should not block")
	}
}
