package proxy

import (
	"fmt"
	"testing"
)

func BenchmarkIsKnownUnreachable_Miss(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = IsKnownUnreachable("example.com", 443)
	}
}

func BenchmarkIsKnownUnreachable_Hit(b *testing.B) {
	MarkUnreachableViaSocket("cf.example", 443)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = IsKnownUnreachable("cf.example", 443)
	}
}

func BenchmarkMarkUnreachable(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MarkUnreachableViaSocket(fmt.Sprintf("h-%d", i&0xff), 443)
	}
}
