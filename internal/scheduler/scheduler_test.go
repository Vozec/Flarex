package scheduler

import (
	"testing"

	"github.com/Vozec/flarex/internal/pool"
)

func TestRoundRobinDistribution(t *testing.T) {
	ws := []*pool.Worker{
		pool.NewWorker("a", "w1", "u"),
		pool.NewWorker("a", "w2", "u"),
		pool.NewWorker("a", "w3", "u"),
	}
	p := pool.New(ws)
	s := NewRoundRobin(p)
	counts := map[string]int{}
	for i := 0; i < 30; i++ {
		w, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		counts[w.Name]++
	}
	for name, c := range counts {
		if c < 8 || c > 12 {
			t.Errorf("%s count %d hors [8..12]", name, c)
		}
	}
}

func TestLeastInflightPicksLowest(t *testing.T) {
	ws := []*pool.Worker{
		pool.NewWorker("a", "w1", "u"),
		pool.NewWorker("a", "w2", "u"),
	}
	ws[0].Inflight.Store(10)
	ws[1].Inflight.Store(2)
	p := pool.New(ws)
	s := NewLeastInflight(p)
	w, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "w2" {
		t.Errorf("should pick w2 (inflight=2), got %s", w.Name)
	}
}
