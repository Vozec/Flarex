package pool

import (
	"testing"
)

func TestNewWorkerDefaults(t *testing.T) {
	w := NewWorker("acc", "name", "https://x.workers.dev")
	if !w.Healthy.Load() {
		t.Error("Healthy default true")
	}
	if w.breaker == nil {
		t.Error("breaker nil")
	}
	if !w.Available() {
		t.Error("fresh worker should be available")
	}
}

func TestEWMARecord(t *testing.T) {
	w := NewWorker("acc", "n", "https://x")
	for i := 0; i < 20; i++ {
		w.RecordResult(true)
	}
	if w.Healthy.Load() {
		t.Error("20 erreurs should mark unhealthy")
	}
	if w.EWMAErrRate() <= healthThreshold {
		t.Errorf("EWMA should be > %f", healthThreshold)
	}
}

func TestPoolRR(t *testing.T) {
	ws := []*Worker{
		NewWorker("a", "w1", "u1"),
		NewWorker("a", "w2", "u2"),
		NewWorker("a", "w3", "u3"),
	}
	p := New(ws)
	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		w, err := p.NextRR()
		if err != nil {
			t.Fatal(err)
		}
		seen[w.Name]++
	}
	if len(seen) != 3 {
		t.Errorf("RR: expected 3 workers visited, got %d", len(seen))
	}
	for name, c := range seen {
		if c != 3 {
			t.Errorf("%s visited %d times (expected 3)", name, c)
		}
	}
}

func TestPoolNextSkipsUnavailable(t *testing.T) {
	ws := []*Worker{
		NewWorker("a", "w1", "u1"),
		NewWorker("a", "w2", "u2"),
	}
	ws[0].Healthy.Store(false)
	p := New(ws)
	for i := 0; i < 5; i++ {
		w, err := p.NextRR()
		if err != nil {
			t.Fatal(err)
		}
		if w.Name != "w2" {
			t.Errorf("should always retourner w2, got %s", w.Name)
		}
	}
}

func TestPoolReplace(t *testing.T) {
	w1 := NewWorker("a", "w1", "u1")
	w2 := NewWorker("a", "w2", "u2")
	p := New([]*Worker{w1, w2})
	w1New := NewWorker("a", "w1new", "u1new")
	p.Replace(w1, w1New)
	all := p.All()
	if all[0].Name != "w1new" {
		t.Errorf("replace failed: got %s", all[0].Name)
	}
	if all[1].Name != "w2" {
		t.Errorf("order changed: got %s", all[1].Name)
	}
}
