package pool

import (
	"testing"
)

func BenchmarkPoolNextRR_Parallel(b *testing.B) {
	const N = 16
	ws := make([]*Worker, N)
	for i := range ws {
		ws[i] = NewWorker("acc", "w", "u")
	}
	p := New(ws)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = p.NextRR()
		}
	})
}

func BenchmarkPoolAll(b *testing.B) {
	const N = 32
	ws := make([]*Worker, N)
	for i := range ws {
		ws[i] = NewWorker("acc", "w", "u")
	}
	p := New(ws)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = p.All()
		}
	})
}
