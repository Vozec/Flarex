package proxy

import "testing"

func TestPickModeForced(t *testing.T) {
	if got := PickMode("socket", "x", 80); got != "socket" {
		t.Errorf("forced socket: %s", got)
	}
	if got := PickMode("fetch", "x", 443); got != "fetch" {
		t.Errorf("forced fetch: %s", got)
	}
}

func TestPickModeHybridPort(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"example.com", 443, "socket"},
		{"example.com", 80, "socket"},
		{"example.com", 8080, "socket"},
		{"example.com", 22, "socket"},
		{"example.com", 6379, "socket"},
	}
	for _, tc := range cases {
		if got := PickMode("hybrid", tc.host, tc.port); got != tc.want {
			t.Errorf("hybrid %s:%d = %s, want %s", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestPickModeHybridAlwaysSocket(t *testing.T) {
	// Byte-sniff decides fetch promotion at runtime; initial pick is always socket.
	for _, c := range []struct {
		h string
		p int
	}{
		{"172.67.74.152", 443},
		{"104.16.0.1", 80},
		{"172.67.74.152", 22},
		{"8.8.8.8", 443},
		{"example.com", 80},
	} {
		if got := PickMode("hybrid", c.h, c.p); got != "socket" {
			t.Errorf("hybrid %s:%d = %s, want socket", c.h, c.p, got)
		}
	}
}

func TestPickModeUnknownDefaults(t *testing.T) {
	if got := PickMode("", "x", 80); got != "socket" {
		t.Errorf("empty mode fallback: %s", got)
	}
	if got := PickMode("nope", "x", 80); got != "socket" {
		t.Errorf("unknown mode fallback: %s", got)
	}
}
