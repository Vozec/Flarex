// Package metrics series.go — in-memory ring buffer of per-minute counter
// snapshots that feeds the admin UI's charts (request rate, dial latency,
// error rate). Complements the Prometheus scrape endpoint (which exposes
// current aggregates only).
package metrics

import (
	"encoding/json"
	"sync"
	"time"
)

// Tiny JSON shims so the metrics package stays dep-light.
func jsonMarshal(v any) ([]byte, error)        { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error      { return json.Unmarshal(b, v) }

// Sample is one per-minute snapshot of the core counters. Deltas between
// consecutive samples give the UI a request-rate series without the
// frontend having to integrate Prometheus.
type Sample struct {
	At              time.Time         `json:"at"`
	Connections     uint64            `json:"connections"`
	DialSuccess     uint64            `json:"dial_success"`
	DialFail        uint64            `json:"dial_fail"`
	HandshakeFail   uint64            `json:"handshake_fail"`
	BytesUpstream   uint64            `json:"bytes_upstream"`
	BytesDownstream uint64            `json:"bytes_downstream"`
	FetchFallback   uint64            `json:"fetch_fallback"`
	HedgeFired      uint64            `json:"hedge_fired"`
	HedgeWins       uint64            `json:"hedge_wins"`
	LatencyP50      float64           `json:"latency_p50_ms"`
	LatencyP95      float64           `json:"latency_p95_ms"`
	LatencyP99      float64           `json:"latency_p99_ms"`
	WorkerRequests  map[string]uint64 `json:"worker_requests,omitempty"`
}

// WorkerRequestsFn is injected from main — returns the current per-worker
// request count (Worker.Requests.Load()). Kept behind an indirection so
// this package doesn't import pool. Set via SetWorkerRequestsFn.
var WorkerRequestsFn func() map[string]uint64

// SetWorkerRequestsFn wires the per-worker counter snapshot. Call from
// main before StartSnapshotLoop.
func SetWorkerRequestsFn(fn func() map[string]uint64) { WorkerRequestsFn = fn }

// Series is a thread-safe ring buffer. Capacity is 60 slots (= one hour of
// per-minute samples); older samples are overwritten.
type Series struct {
	mu      sync.RWMutex
	samples []Sample
	head    int // next write position
	full    bool
}

// DefaultSeries is the package-global ring. Populated by StartSnapshotLoop.
// Admin server reads it via Snapshot() for /metrics/series responses.
var DefaultSeries = NewSeries(60)

// NewSeries creates a ring with the given capacity. capacity=0 → 60.
func NewSeries(capacity int) *Series {
	if capacity <= 0 {
		capacity = 60
	}
	return &Series{samples: make([]Sample, capacity)}
}

// Push writes a sample to the ring.
func (s *Series) Push(sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples[s.head] = sample
	s.head = (s.head + 1) % len(s.samples)
	if s.head == 0 {
		s.full = true
	}
}

// Snapshot returns a copy of the samples in chronological order (oldest
// first). The returned slice is safe to serialize.
func (s *Series) Snapshot() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.head
	if s.full {
		n = len(s.samples)
	}
	out := make([]Sample, 0, n)
	if !s.full {
		out = append(out, s.samples[:s.head]...)
		return out
	}
	out = append(out, s.samples[s.head:]...)
	out = append(out, s.samples[:s.head]...)
	return out
}

// Persistor is the minimal interface the snapshot loop needs to persist
// samples across restarts. nil = in-memory only (ring dies on exit).
type Persistor interface {
	PutMetricsSample(at time.Time, raw []byte) error
	ListMetricsSamples(since time.Time) ([][]byte, error)
	PruneMetricsSamples(before time.Time) error
}

// StartSnapshotLoop pushes a Sample every `interval` until the returned
// stop function is called. Reads current counter values from the package
// globals. latencyQuantileFn is injected so this file stays decoupled
// from the histogram implementation. `persist` is optional — when non-nil,
// every sample is also written to bbolt and the ring is pre-filled at
// boot from the persistent backlog (last `interval * len(samples)`).
func StartSnapshotLoop(interval time.Duration, latencyQuantileFn func(q float64) float64, persist Persistor) func() {
	if interval <= 0 {
		interval = time.Minute
	}
	// Pre-fill the ring from persisted samples so a restart doesn't blank
	// the chart. Cap the window to the ring capacity × interval.
	if persist != nil {
		since := time.Now().Add(-time.Duration(len(DefaultSeries.samples)) * interval)
		raws, err := persist.ListMetricsSamples(since)
		if err == nil {
			for _, raw := range raws {
				var s Sample
				if jsonUnmarshal(raw, &s) == nil {
					DefaultSeries.Push(s)
				}
			}
		}
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		// Periodic pruning: keep 7 days of persisted samples, no more.
		pruneEvery := time.NewTicker(1 * time.Hour)
		defer pruneEvery.Stop()

		push := func() {
			s := snapshotNow(latencyQuantileFn)
			DefaultSeries.Push(s)
			if persist != nil {
				if raw, err := jsonMarshal(s); err == nil {
					_ = persist.PutMetricsSample(s.At, raw)
				}
			}
		}
		push() // first sample right away
		for {
			select {
			case <-t.C:
				push()
			case <-pruneEvery.C:
				if persist != nil {
					_ = persist.PruneMetricsSamples(time.Now().AddDate(0, 0, -7))
				}
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

func snapshotNow(q func(float64) float64) Sample {
	if q == nil {
		q = func(_ float64) float64 { return 0 }
	}
	s := Sample{
		At:              time.Now().UTC(),
		Connections:     ConnectionsTotal.Get(),
		DialSuccess:     DialSuccessTotal.Get(),
		DialFail:        DialFailTotal.Get(),
		HandshakeFail:   HandshakeFailTotal.Get(),
		BytesUpstream:   BytesUpstream.Get(),
		BytesDownstream: BytesDownstream.Get(),
		FetchFallback:   FetchFallbackTotal.Get(),
		HedgeFired:      HedgeFiredTotal.Get(),
		HedgeWins:       HedgeWinsTotal.Get(),
		LatencyP50:      q(0.5) * 1000,
		LatencyP95:      q(0.95) * 1000,
		LatencyP99:      q(0.99) * 1000,
	}
	if WorkerRequestsFn != nil {
		s.WorkerRequests = WorkerRequestsFn()
	}
	return s
}
