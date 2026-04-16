package dnscache

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Cache struct {
	resolver *net.Resolver
	dialer   *net.Dialer
	entries  sync.Map
	ttl      time.Duration
}

func New(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Cache{
		resolver: net.DefaultResolver,
		dialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		ttl: ttl,
	}
}

func (c *Cache) Warm(ctx context.Context, hostnames []string) {
	var wg sync.WaitGroup
	for _, h := range hostnames {
		h := h
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.lookup(ctx, h)
		}()
	}
	wg.Wait()
}

func (c *Cache) RefreshLoop(ctx context.Context) {
	t := time.NewTicker(c.ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.entries.Range(func(k, _ any) bool {
				_, _ = c.lookup(ctx, k.(string))
				return true
			})
		}
	}
}

func (c *Cache) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := c.lookup(ctx, host)
	if err != nil || len(ips) == 0 {
		return c.dialer.DialContext(ctx, network, address)
	}

	var lastErr error
	for _, ip := range ips {
		conn, err := c.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dnscache: all IPs failed for %s: %w", host, lastErr)
}

func (c *Cache) lookup(ctx context.Context, host string) ([]net.IP, error) {

	if v, ok := c.entries.Load(host); ok {
		ptr := v.(*atomic.Pointer[[]net.IP])
		if cached := ptr.Load(); cached != nil && len(*cached) > 0 {
			return *cached, nil
		}
	}
	addrs, err := c.resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	ptr := new(atomic.Pointer[[]net.IP])
	ptr.Store(&addrs)
	c.entries.Store(host, ptr)
	return addrs, nil
}

// LookupIPv4 returns the first IPv4 address for host, or "" if none
// (synchronous, uses cache). Used by the proxy's `prefer_ipv4` path
// to pass an A-record literal to the Worker instead of a hostname.
// Failure modes (DNS error, no A records) return "" — the caller then
// falls back to the original hostname.
func (c *Cache) LookupIPv4(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := c.lookup(ctx, host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

func (c *Cache) Stats() map[string]int {
	out := map[string]int{}
	c.entries.Range(func(k, v any) bool {
		ptr := v.(*atomic.Pointer[[]net.IP])
		if cached := ptr.Load(); cached != nil {
			out[k.(string)] = len(*cached)
		}
		return true
	})
	return out
}
