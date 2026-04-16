package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Vozec/flarex/internal/auth"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/coder/websocket"
)

var wsHTTPClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	},
}

var ErrUpstreamBlocked = errors.New("upstream blocked (likely CF-hosted target)")

// Runtime-tunable settings populated once at boot from config.
var (
	ProbeDisabled     bool
	DialTimeout       = 15 * time.Second
	ProbeTimeout      = 800 * time.Millisecond
	ProbeFetchTimeout = 1500 * time.Millisecond
	// PreferIPv4, when true + PreferIPv4Resolver set, pre-resolves the
	// target hostname to an IPv4 address and passes the literal to the
	// Worker. Works around `cloudflare:sockets.connect()` doing its own
	// DNS (can't be forced to prefer A over AAAA).
	PreferIPv4         bool
	PreferIPv4Resolver func(host string) string
)

func DialWorker(ctx context.Context, w *pool.Worker, hmacSecret, host string, port int, tlsUpstream bool, mode string) (net.Conn, error) {
	if PreferIPv4 && PreferIPv4Resolver != nil {
		if ipv4 := PreferIPv4Resolver(host); ipv4 != "" {
			host = ipv4
		}
	}
	if mode == "" {
		mode = ModeSocket
	}
	ts, sig := auth.Sign(hmacSecret, host, port, tlsUpstream, mode)

	u, err := url.Parse(w.URL)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	q := u.Query()
	q.Set("h", host)
	q.Set("p", fmt.Sprintf("%d", port))
	if tlsUpstream {
		q.Set("t", "1")
	} else {
		q.Set("t", "0")
	}
	q.Set("ts", fmt.Sprintf("%d", ts))
	q.Set("s", sig)
	q.Set("m", mode)
	u.RawQuery = q.Encode()

	dialCtx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()

	client := w.HTTPClient()
	if client == nil {
		client = wsHTTPClient
	}
	ws, _, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{
		HTTPClient: client,
	})
	if err != nil {
		return nil, fmt.Errorf("ws dial worker: %w", err)
	}
	ws.SetReadLimit(-1)

	if ProbeDisabled {
		return &wsConn{ws: ws, ctx: context.Background()}, nil
	}
	probeTimeout := ProbeTimeout
	if mode == ModeFetch {
		probeTimeout = ProbeFetchTimeout
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, probeTimeout)
	defer probeCancel()
	_, probe, rerr := ws.Read(probeCtx)
	if rerr != nil {
		_ = ws.Close(websocket.StatusNormalClosure, "")
		if cerr := websocket.CloseStatus(rerr); cerr == 4001 {
			return nil, ErrUpstreamBlocked
		}
		return nil, fmt.Errorf("ws probe: %w", rerr)
	}
	if len(probe) != 1 || probe[0] != 0x01 {
		_ = ws.Close(websocket.StatusNormalClosure, "")
		return nil, fmt.Errorf("ws probe: unexpected signature (len=%d)", len(probe))
	}

	return &wsConn{ws: ws, ctx: context.Background()}, nil
}

type wsConn struct {
	ws     *websocket.Conn
	ctx    context.Context
	rr     io.Reader
	closed bool
	mu     sync.Mutex
}

func (w *wsConn) Read(b []byte) (int, error) {

	if w.rr != nil {
		n, err := w.rr.Read(b)
		if err == io.EOF {
			w.rr = nil
			if n > 0 {
				return n, nil
			}

		} else {
			return n, err
		}
	}
	mt, rr, err := w.ws.Reader(w.ctx)
	if err != nil {
		return 0, err
	}
	if mt != websocket.MessageBinary && mt != websocket.MessageText {

		return 0, io.EOF
	}
	w.rr = rr
	return w.rr.Read(b)
}

func (w *wsConn) Write(b []byte) (int, error) {

	if err := w.ws.Write(w.ctx, websocket.MessageBinary, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (w *wsConn) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.ws.Close(websocket.StatusNormalClosure, "")
}

func (w *wsConn) LocalAddr() net.Addr               { return dummyAddr("ws-local") }
func (w *wsConn) RemoteAddr() net.Addr              { return dummyAddr("ws-remote") }
func (w *wsConn) SetDeadline(t time.Time) error     { return nil }
func (w *wsConn) SetReadDeadline(t time.Time) error { return nil }
func (w *wsConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type dummyAddr string

func (d dummyAddr) Network() string { return "ws" }
func (d dummyAddr) String() string  { return string(d) }
