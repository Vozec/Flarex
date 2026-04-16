package pool

import (
	"fmt"
	"testing"
)

func buildPool(n int) *Pool {
	ws := make([]*Worker, n)
	for i := 0; i < n; i++ {
		ws[i] = NewWorker("acc", fmt.Sprintf("w%d", i), "https://x")
	}
	return New(ws)
}

func TestNextBySession_Sticky(t *testing.T) {
	p := buildPool(8)
	w1, err := p.NextBySession("user-abc")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := p.NextBySession("user-abc")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != w1.Name {
			t.Fatalf("session drifted to %s (was %s)", got.Name, w1.Name)
		}
	}
}

func TestNextBySession_DifferentKeys(t *testing.T) {
	p := buildPool(16)
	seen := map[string]int{}
	for i := 0; i < 100; i++ {
		w, err := p.NextBySession(fmt.Sprintf("sess-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		seen[w.Name]++
	}
	if len(seen) < 4 {
		t.Errorf("FNV distribution poor: only %d unique workers over 100 keys", len(seen))
	}
}

func TestNextBySession_FallbackOnUnhealthy(t *testing.T) {
	p := buildPool(4)
	w1, _ := p.NextBySession("my-session")
	w1.Healthy.Store(false)
	w2, err := p.NextBySession("my-session")
	if err != nil {
		t.Fatal(err)
	}
	if w2.Name == w1.Name {
		t.Errorf("unhealthy worker still returned: %s", w2.Name)
	}
}

func TestNextBySession_AllUnhealthy(t *testing.T) {
	p := buildPool(3)
	for _, w := range p.All() {
		w.Healthy.Store(false)
	}
	if _, err := p.NextBySession("s"); err == nil {
		t.Error("expected error when no workers available")
	}
}

func TestNextBySession_EmptyPool(t *testing.T) {
	p := New(nil)
	if _, err := p.NextBySession("s"); err == nil {
		t.Error("expected error on empty pool")
	}
}

func TestNextBySession_EmptyKey(t *testing.T) {
	p := buildPool(4)
	// Empty session key should still pick deterministically (hash of "").
	w, err := p.NextBySession("")
	if err != nil {
		t.Fatal(err)
	}
	w2, _ := p.NextBySession("")
	if w.Name != w2.Name {
		t.Errorf("empty key not deterministic: %s vs %s", w.Name, w2.Name)
	}
}
