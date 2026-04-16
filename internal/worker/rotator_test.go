package worker

import (
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/pool"
)

func TestShouldRecycleAge(t *testing.T) {
	w := pool.NewWorker("a", "w1", "u1")
	w.CreatedAt = time.Now().Add(-2 * time.Hour)
	if !shouldRecycle(w, time.Hour, 0) {
		t.Error("2h old > 1h max should recycle")
	}
	if shouldRecycle(w, 3*time.Hour, 0) {
		t.Error("2h old < 3h max should NOT recycle")
	}
}

func TestShouldRecycleReqs(t *testing.T) {
	w := pool.NewWorker("a", "w1", "u1")
	w.Requests.Store(1500)
	if !shouldRecycle(w, 0, 1000) {
		t.Error("1500 reqs > 1000 max should recycle")
	}
	if shouldRecycle(w, 0, 2000) {
		t.Error("1500 reqs < 2000 max should NOT recycle")
	}
}

func TestShouldRecycleBothOff(t *testing.T) {
	w := pool.NewWorker("a", "w1", "u1")
	w.CreatedAt = time.Now().Add(-100 * time.Hour)
	w.Requests.Store(1_000_000)
	if shouldRecycle(w, 0, 0) {
		t.Error("both 0 = no recycle (off)")
	}
}

func TestRandHex(t *testing.T) {
	for n := 1; n <= 8; n++ {
		got := randHex(n)
		if len(got) != n*2 {
			t.Errorf("len(randHex(%d))=%d", n, len(got))
		}
	}
}
