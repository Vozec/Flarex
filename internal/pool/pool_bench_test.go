package pool

import (
	"fmt"
	"testing"
)

func makePool(n int, allHealthy bool) *Pool {
	ws := make([]*Worker, n)
	for i := 0; i < n; i++ {
		w := NewWorker("acc", fmt.Sprintf("w-%d", i), "https://w.example/")
		if !allHealthy && i%4 == 0 {
			w.Healthy.Store(false)
		}
		ws[i] = w
	}
	return New(ws)
}

func BenchmarkNextRR_Healthy(b *testing.B) {
	p := makePool(32, true)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = p.NextRR()
	}
}

func BenchmarkNextRR_PartialUnhealthy(b *testing.B) {
	p := makePool(32, false)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = p.NextRR()
	}
}

func BenchmarkNextRR_Parallel(b *testing.B) {
	p := makePool(64, true)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = p.NextRR()
		}
	})
}
