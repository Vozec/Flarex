package filter

import (
	"fmt"
	"net/netip"
	"sync"

	"go4.org/netipx"
)

var defaultDenyCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"100.64.0.0/10",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"0.0.0.0/8",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
}

type IPFilter struct {
	set        *netipx.IPSet
	allowPorts map[int]struct{}
	allowAll   bool
	mu         sync.RWMutex // guards allowPorts/allowAll on hot-swap from admin UI
}

func NewIPFilter(extraDeny []string, allowPorts []any) (*IPFilter, error) {
	var b netipx.IPSetBuilder
	// Don't append into defaultDenyCIDRs: that would mutate the package-
	// level slice if cap allowed it. Build a fresh slice each call.
	all := make([]string, 0, len(defaultDenyCIDRs)+len(extraDeny))
	all = append(all, defaultDenyCIDRs...)
	all = append(all, extraDeny...)
	for _, s := range all {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("parse cidr %q: %w", s, err)
		}
		b.AddPrefix(p)
	}
	set, err := b.IPSet()
	if err != nil {
		return nil, err
	}
	ports := make(map[int]struct{}, len(allowPorts))
	allowAll := false
	for _, raw := range allowPorts {
		switch v := raw.(type) {
		case string:
			if v == "*" {
				allowAll = true
				continue
			}

			var p int
			if _, err := fmt.Sscanf(v, "%d", &p); err != nil {
				return nil, fmt.Errorf("bad port %q", v)
			}
			ports[p] = struct{}{}
		case int:
			if v == 0 {
				allowAll = true
				continue
			}
			ports[v] = struct{}{}
		case int64:
			if v == 0 {
				allowAll = true
				continue
			}
			ports[int(v)] = struct{}{}
		case float64:
			if v == 0 {
				allowAll = true
				continue
			}
			ports[int(v)] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported port type %T: %v", raw, raw)
		}
	}
	return &IPFilter{set: set, allowPorts: ports, allowAll: allowAll}, nil
}

func (f *IPFilter) AllowPort(port int) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.allowAll || len(f.allowPorts) == 0 {
		return true
	}
	_, ok := f.allowPorts[port]
	return ok
}

// SetAllowPorts atomically swaps the port allowlist. Used by the admin UI's
// PATCH /config endpoint so changes to filter.allow_ports take effect on
// new dials immediately, without restart.
func (f *IPFilter) SetAllowPorts(raw []any) error {
	ports := make(map[int]struct{}, len(raw))
	allowAll := false
	for _, r := range raw {
		switch v := r.(type) {
		case string:
			if v == "*" {
				allowAll = true
				continue
			}
			var p int
			if _, err := fmt.Sscanf(v, "%d", &p); err != nil {
				return fmt.Errorf("bad port %q", v)
			}
			ports[p] = struct{}{}
		case int:
			if v == 0 {
				allowAll = true
				continue
			}
			ports[v] = struct{}{}
		case int64:
			if v == 0 {
				allowAll = true
				continue
			}
			ports[int(v)] = struct{}{}
		case float64:
			if v == 0 {
				allowAll = true
				continue
			}
			ports[int(v)] = struct{}{}
		default:
			return fmt.Errorf("unsupported port type %T", r)
		}
	}
	f.mu.Lock()
	f.allowPorts = ports
	f.allowAll = allowAll
	f.mu.Unlock()
	return nil
}

func (f *IPFilter) AllowAddr(a netip.Addr) bool {
	return !f.set.Contains(a)
}

func (f *IPFilter) AllowHost(host string, port int) error {
	if !f.AllowPort(port) {
		return fmt.Errorf("port %d not allowed", port)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if !f.AllowAddr(a) {
			return fmt.Errorf("ip %s in deny-list", a)
		}
	}

	return nil
}
