package pool

import (
	"testing"
	"time"
)

func TestQuotaIncrement(t *testing.T) {
	q := NewQuota("acc", 5)
	for i := uint64(1); i <= 5; i++ {
		n, hit := q.Inc()
		if n != i {
			t.Errorf("Inc %d: n=%d", i, n)
		}
		if hit != (i >= 5) {
			t.Errorf("Inc %d: hit=%v", i, hit)
		}
	}
}

func TestQuotaUnlimited(t *testing.T) {
	q := NewQuota("acc", 0)
	for i := 0; i < 100; i++ {
		_, hit := q.Inc()
		if hit {
			t.Error("unlimited should never hit")
		}
	}
	if q.Remaining() != 0 {
		t.Error("remaining = 0 pour unlimited (sentinelle)")
	}
}

func TestQuotaRemaining(t *testing.T) {
	q := NewQuota("acc", 10)
	q.Inc()
	q.Inc()
	q.Inc()
	if r := q.Remaining(); r != 7 {
		t.Errorf("remaining = %d, want 7", r)
	}
}

func TestQuotaResetNextDay(t *testing.T) {
	q := NewQuota("acc", 100)
	q.Inc()
	q.Inc()

	q.DayStart.Store(startOfDay(time.Now().Add(-25 * time.Hour)))

	n, _ := q.Inc()
	if n != 1 {
		t.Errorf("after reset: n=%d, want 1", n)
	}
}
