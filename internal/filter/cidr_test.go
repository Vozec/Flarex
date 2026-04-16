package filter

import (
	"net/netip"
	"testing"
)

func TestDenyDefaults(t *testing.T) {
	f, err := NewIPFilter(nil, []any{80, 443})
	if err != nil {
		t.Fatal(err)
	}
	denied := []string{
		"127.0.0.1",
		"10.0.0.5",
		"172.16.1.1",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.0.1",
		"::1",
		"fe80::1",
		"fc00::1",
	}
	for _, s := range denied {
		a, _ := netip.ParseAddr(s)
		if f.AllowAddr(a) {
			t.Errorf("%s should be denied", s)
		}
	}
}

func TestAllowPublic(t *testing.T) {
	f, _ := NewIPFilter(nil, []any{80, 443})
	allowed := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, s := range allowed {
		a, _ := netip.ParseAddr(s)
		if !f.AllowAddr(a) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestAllowPort(t *testing.T) {
	f, _ := NewIPFilter(nil, []any{80, 443})
	if !f.AllowPort(80) {
		t.Error("port 80 should be allowed")
	}
	if f.AllowPort(22) {
		t.Error("port 22 should be denied")
	}
}

func TestAllowPortEmpty(t *testing.T) {
	f, _ := NewIPFilter(nil, nil)
	if !f.AllowPort(22) {
		t.Error("empty list = all allowed")
	}
}

func TestAllowPortWildcard(t *testing.T) {
	f, err := NewIPFilter(nil, []any{"*"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{1, 22, 80, 443, 8080, 65535} {
		if !f.AllowPort(p) {
			t.Errorf("wildcard should allow %d", p)
		}
	}
}

func TestAllowPortMixedTypes(t *testing.T) {
	f, err := NewIPFilter(nil, []any{80, int64(443), 8080.0, "9090"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{80, 443, 8080, 9090} {
		if !f.AllowPort(p) {
			t.Errorf("should allow %d", p)
		}
	}
	if f.AllowPort(22) {
		t.Error("22 not in list")
	}
}

func TestAllowHostHostname(t *testing.T) {
	f, _ := NewIPFilter(nil, []any{443})
	if err := f.AllowHost("example.com", 443); err != nil {
		t.Errorf("public hostname should pass, got %v", err)
	}
	if err := f.AllowHost("example.com", 22); err == nil {
		t.Error("blocked port should error")
	}
}

func TestAllowHostIP(t *testing.T) {
	f, _ := NewIPFilter(nil, []any{443})
	if err := f.AllowHost("127.0.0.1", 443); err == nil {
		t.Error("loopback should error")
	}
	if err := f.AllowHost("1.1.1.1", 443); err != nil {
		t.Errorf("public IP should pass, got %v", err)
	}
}

func TestExtraDeny(t *testing.T) {
	f, err := NewIPFilter([]string{"8.8.8.0/24"}, []any{443})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := netip.ParseAddr("8.8.8.8")
	if f.AllowAddr(a) {
		t.Error("8.8.8.8 should be denied via extra")
	}
}
