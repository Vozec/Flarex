package pool

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker/v2"
	"golang.org/x/net/http2"
)

type Worker struct {
	AccountID   string
	Name        string
	URL         string
	Inflight    atomic.Int64
	Errors      atomic.Uint64
	Requests    atomic.Uint64
	Healthy     atomic.Bool
	QuotaPaused atomic.Bool

	// Colo holds the CF PoP colocation code the Worker reports via its
	// /__health endpoint's X-CF-Colo header. Optional; empty until the
	// first successful health ping.
	Colo atomic.Pointer[string]

	EWMAErr atomic.Uint64

	CreatedAt time.Time

	Backend  string
	Hostname string
	ZoneID   string
	RecordID string

	breaker *gobreaker.CircuitBreaker[struct{}]

	httpClient atomic.Pointer[http.Client]
	clientOnce sync.Once
}

func NewWorker(accountID, name, url string) *Worker {
	w := &Worker{
		AccountID: accountID,
		Name:      name,
		URL:       url,
		CreatedAt: time.Now(),
	}
	w.Healthy.Store(true)
	w.breaker = gobreaker.NewCircuitBreaker[struct{}](gobreaker.Settings{
		Name:        name,
		MaxRequests: 3,
		Interval:    BreakerIntervalD,
		Timeout:     BreakerOpenTimeoutD,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5 ||
				(counts.Requests >= 10 && float64(counts.TotalFailures)/float64(counts.Requests) > 0.5)
		},
	})
	return w
}

var (
	SharedDialer        func(ctx context.Context, network, address string) (net.Conn, error)
	BreakerIntervalD    = 60 * time.Second
	BreakerOpenTimeoutD = 30 * time.Second
	HTTPIdleConnD       = 120 * time.Second
	HTTPTLSHandshakeD   = 10 * time.Second
	HTTPH2ReadIdleD     = 30 * time.Second
	HTTPH2PingD         = 10 * time.Second
)

func newTunedHTTPClient() *http.Client {
	base := &http.Transport{
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       HTTPIdleConnD,
		TLSHandshakeTimeout:   HTTPTLSHandshakeD,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		ReadBufferSize:        256 << 10,
		WriteBufferSize:       256 << 10,
	}
	if SharedDialer != nil {
		base.DialContext = SharedDialer
	}
	if h2, err := http2.ConfigureTransports(base); err == nil && h2 != nil {
		h2.MaxReadFrameSize = 1 << 20
		h2.ReadIdleTimeout = HTTPH2ReadIdleD
		h2.PingTimeout = HTTPH2PingD
		h2.StrictMaxConcurrentStreams = false
		h2.MaxHeaderListSize = 1 << 16
		h2.AllowHTTP = false
	}
	return &http.Client{Transport: base, Timeout: 0}
}

func (w *Worker) HTTPClient() *http.Client {
	w.clientOnce.Do(func() {
		w.httpClient.Store(newTunedHTTPClient())
	})
	return w.httpClient.Load()
}

func (w *Worker) Breaker() *gobreaker.CircuitBreaker[struct{}] { return w.breaker }

func (w *Worker) BreakerState() string {
	return w.breaker.State().String()
}

func (w *Worker) Available() bool {
	if w.QuotaPaused.Load() {
		return false
	}
	return w.Healthy.Load() && w.breaker.State() != gobreaker.StateOpen
}

// ColoString returns the last-observed CF colo code or "" if unknown.
// Safe to call concurrently.
func (w *Worker) ColoString() string {
	if p := w.Colo.Load(); p != nil {
		return *p
	}
	return ""
}

// SetColo stores a new colo code. Pass "" to clear.
func (w *Worker) SetColo(colo string) {
	w.Colo.Store(&colo)
}

type Pool struct {
	snap   atomic.Pointer[[]*Worker]
	quotas map[string]*Quota
	rr     atomic.Uint64
	mu     sync.Mutex
}

func New(workers []*Worker) *Pool {
	p := &Pool{}
	cp := append([]*Worker(nil), workers...)
	p.snap.Store(&cp)
	return p
}

func (p *Pool) load() []*Worker {
	if s := p.snap.Load(); s != nil {
		return *s
	}
	return nil
}

func (p *Pool) store(ws []*Worker) {
	p.snap.Store(&ws)
}

func (p *Pool) SetQuotas(q map[string]*Quota) { p.quotas = q }

func (p *Pool) QuotaFor(accountID string) *Quota { return p.quotas[accountID] }

func (p *Pool) Size() int { return len(p.load()) }

func (p *Pool) All() []*Worker { return p.load() }

func (p *Pool) Replace(old, new *Worker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.load()
	cp := make([]*Worker, len(cur))
	copy(cp, cur)
	for i, w := range cp {
		if w == old {
			cp[i] = new
			p.store(cp)
			return
		}
	}
}

func (p *Pool) Add(w *Worker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.load()
	cp := make([]*Worker, len(cur)+1)
	copy(cp, cur)
	cp[len(cur)] = w
	p.store(cp)
}

func (p *Pool) Remove(w *Worker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.load()
	for i, x := range cur {
		if x == w {
			cp := make([]*Worker, 0, len(cur)-1)
			cp = append(cp, cur[:i]...)
			cp = append(cp, cur[i+1:]...)
			p.store(cp)
			return
		}
	}
}

// SetAccountPaused mass-toggles the QuotaPaused flag for every worker on
// the given account. Returns the number of workers updated. Used by the
// admin API to temporarily remove an account's workers from rotation
// without destroying them. Also mirrors Healthy to match — paused workers
// don't serve new requests.
func (p *Pool) SetAccountPaused(accountID string, paused bool) int {
	n := 0
	for _, w := range p.ByAccount(accountID) {
		w.QuotaPaused.Store(paused)
		if paused {
			w.Healthy.Store(false)
		} else {
			w.Healthy.Store(true)
		}
		n++
	}
	return n
}

func (p *Pool) ByAccount(accountID string) []*Worker {
	cur := p.load()
	out := make([]*Worker, 0, len(cur))
	for _, w := range cur {
		if w.AccountID == accountID {
			out = append(out, w)
		}
	}
	return out
}

func (p *Pool) NextRR() (*Worker, error) {
	cur := p.load()
	if len(cur) == 0 {
		return nil, fmt.Errorf("empty pool")
	}
	start := p.rr.Add(1) - 1
	n := uint64(len(cur))
	for i := uint64(0); i < n; i++ {
		w := cur[int((start+i)%n)]
		if w.Available() {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no workers available (all unhealthy or breaker open)")
}

// NextBySession hashes session to a pool index and returns the first available
// worker starting from that index. Same session → same worker while healthy.
// Falls back to round-robin sweep if the anchored worker is unavailable.
func (p *Pool) NextBySession(session string) (*Worker, error) {
	cur := p.load()
	if len(cur) == 0 {
		return nil, fmt.Errorf("empty pool")
	}
	// Inline FNV-1a to avoid allocating a hash.Hash64 per call.
	start := uint64(14695981039346656037)
	for i := 0; i < len(session); i++ {
		start ^= uint64(session[i])
		start *= 1099511628211
	}
	n := uint64(len(cur))
	for i := uint64(0); i < n; i++ {
		w := cur[int((start+i)%n)]
		if w.Available() {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no workers available")
}

func (p *Pool) NextSkip(skip map[string]struct{}) (*Worker, error) {
	cur := p.load()
	if len(cur) == 0 {
		return nil, fmt.Errorf("empty pool")
	}
	start := p.rr.Add(1) - 1
	n := uint64(len(cur))
	for i := uint64(0); i < n; i++ {
		w := cur[int((start+i)%n)]
		if !w.Available() {
			continue
		}
		if _, skipped := skip[w.Name]; skipped {
			continue
		}
		return w, nil
	}
	return nil, fmt.Errorf("no more workers available (all tried or unavailable)")
}
