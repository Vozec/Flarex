package pool

import (
	"sync/atomic"
	"time"
)

type Quota struct {
	AccountID string

	Reqs atomic.Uint64

	DayStart atomic.Int64

	Limit uint64
}

func NewQuota(accountID string, limit uint64) *Quota {
	q := &Quota{AccountID: accountID, Limit: limit}
	q.DayStart.Store(startOfDay(time.Now()))
	return q
}

func (q *Quota) Seed(initial uint64) {
	q.maybeReset()
	q.Reqs.Store(initial)
}

func (q *Quota) Inc() (uint64, bool) {
	q.maybeReset()
	n := q.Reqs.Add(1)
	if q.Limit > 0 && n >= q.Limit {
		return n, true
	}
	return n, false
}

func (q *Quota) Used() uint64 {
	q.maybeReset()
	return q.Reqs.Load()
}

func (q *Quota) Remaining() uint64 {
	if q.Limit == 0 {
		return 0
	}
	used := q.Used()
	if used >= q.Limit {
		return 0
	}
	return q.Limit - used
}

func (q *Quota) maybeReset() {
	now := time.Now()
	cur := q.DayStart.Load()
	today := startOfDay(now)
	if today > cur {
		if q.DayStart.CompareAndSwap(cur, today) {
			q.Reqs.Store(0)
		}
	}
}

func startOfDay(t time.Time) int64 {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
}
