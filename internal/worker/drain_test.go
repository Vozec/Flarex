package worker

import (
	"context"
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/pool"
)

func TestDrain_ReturnsWhenInflightZero(t *testing.T) {
	old := DrainTimeoutD
	oldPoll := DrainPollD
	DrainTimeoutD = 500 * time.Millisecond
	DrainPollD = 10 * time.Millisecond
	defer func() { DrainTimeoutD = old; DrainPollD = oldPoll }()

	w := pool.NewWorker("acc", "w1", "https://x")
	w.Inflight.Store(0)
	start := time.Now()
	drain(context.Background(), w)
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("drain took too long when inflight=0: %v", time.Since(start))
	}
}

func TestDrain_TimeoutWhenStuck(t *testing.T) {
	old := DrainTimeoutD
	oldPoll := DrainPollD
	DrainTimeoutD = 100 * time.Millisecond
	DrainPollD = 10 * time.Millisecond
	defer func() { DrainTimeoutD = old; DrainPollD = oldPoll }()

	w := pool.NewWorker("acc", "w1", "https://x")
	w.Inflight.Store(3)
	start := time.Now()
	drain(context.Background(), w)
	elapsed := time.Since(start)
	if elapsed < DrainTimeoutD {
		t.Errorf("drain returned too fast: %v", elapsed)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("drain hung past timeout: %v", elapsed)
	}
}

func TestDrain_DrainsWhenInflightDrops(t *testing.T) {
	old := DrainTimeoutD
	oldPoll := DrainPollD
	DrainTimeoutD = 500 * time.Millisecond
	DrainPollD = 5 * time.Millisecond
	defer func() { DrainTimeoutD = old; DrainPollD = oldPoll }()

	w := pool.NewWorker("acc", "w1", "https://x")
	w.Inflight.Store(2)
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.Inflight.Store(0)
	}()
	start := time.Now()
	drain(context.Background(), w)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("drain slow to notice inflight drop: %v", elapsed)
	}
}

func TestDrain_RespectCtxCancel(t *testing.T) {
	old := DrainTimeoutD
	oldPoll := DrainPollD
	DrainTimeoutD = 10 * time.Second
	DrainPollD = 5 * time.Millisecond
	defer func() { DrainTimeoutD = old; DrainPollD = oldPoll }()

	w := pool.NewWorker("acc", "w1", "https://x")
	w.Inflight.Store(5)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	drain(ctx, w)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("drain ignored ctx cancel: %v", elapsed)
	}
}
