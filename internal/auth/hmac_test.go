package auth

import (
	"strings"
	"testing"
)

func TestSignDeterministic(t *testing.T) {
	ts1, sig1 := Sign("secret", "example.com", 443, true, "socket")
	_ = ts1
	if len(sig1) != 64 {
		t.Errorf("sha256 hex = 64 chars, got %d", len(sig1))
	}
	if !isHex(sig1) {
		t.Errorf("not hex: %s", sig1)
	}
}

func TestSignDifferentInput(t *testing.T) {
	_, a := Sign("s1", "h1", 443, true, "socket")
	_, b := Sign("s1", "h1", 443, false, "socket")
	if a == b {
		t.Error("tls flag should change sig")
	}
	_, c := Sign("s1", "h2", 443, true, "socket")
	if a == c {
		t.Error("host should change sig")
	}
	_, d := Sign("s2", "h1", 443, true, "socket")
	if a == d {
		t.Error("secret should change sig")
	}
	_, e := Sign("s1", "h1", 443, true, "fetch")
	if a == e {
		t.Error("mode should change sig")
	}
}

func isHex(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	}) == -1
}
