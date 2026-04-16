package auth

import "testing"

func BenchmarkSign(b *testing.B) {
	secret := "32-byte-random-secret-for-bench-only"
	host := "example.com"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = Sign(secret, host, 443, true, "socket")
	}
}
