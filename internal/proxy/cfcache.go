package proxy

import (
	"sync"
	"time"
)

var (
	cfUnreachable sync.Map
	// CFCacheTTL is tunable via config; defaults applied in applyResolvedTimeouts.
	CFCacheTTL = 10 * time.Minute
)

// MarkUnreachableViaSocket remembers that connect() fails for this host:port.
// Subsequent PickMode calls will prefer fetch for this target.
func MarkUnreachableViaSocket(host string, port int) {
	key := hostPortKey(host, port)
	cfUnreachable.Store(key, time.Now())
}

// IsKnownUnreachable returns true if host:port was recently marked as failing
// via connect() (e.g. hosted on Cloudflare IPs).
func IsKnownUnreachable(host string, port int) bool {
	v, ok := cfUnreachable.Load(hostPortKey(host, port))
	if !ok {
		return false
	}
	if time.Since(v.(time.Time)) > CFCacheTTL {
		cfUnreachable.Delete(hostPortKey(host, port))
		return false
	}
	return true
}

func hostPortKey(host string, port int) string {
	return host + ":" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [10]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
