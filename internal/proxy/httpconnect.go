package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/metrics"
)

// ServeHTTP runs an HTTP CONNECT proxy listener on ln. It shares the same
// dial path (pool, filter, scheduler, rate-limit, quota hook) as SOCKS5.
// Supports CONNECT host:port HTTP/1.1 (tunnel) and falls back to 502 for
// absolute-form GET/POST requests (use CONNECT).
func (s *Server) ServeHTTP(ctx context.Context, ln net.Listener) error {
	s.poolOnce.Do(func() {})
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		go s.handleHTTP(ctx, conn)
	}
}

func (s *Server) handleHTTP(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
	}
	metrics.ConnectionsTotal.Inc()
	metrics.ConnectionsActive.Inc()
	defer metrics.ConnectionsActive.Dec()

	reqID := randID()
	t0 := time.Now()
	br := bufio.NewReader(conn)

	req, authRaw, err := readHTTPRequest(br)
	if err != nil {
		return
	}
	if req.method != "CONNECT" {
		writeHTTPStatus(conn, 405, "use CONNECT method")
		return
	}

	var sessionID string
	if s.Auth != nil && (s.Auth.User != "" || s.Auth.Pass != "") {
		u, p, ok := decodeBasicAuth(authRaw)
		if !ok || !authMatches(u, s.Auth.User) || p != s.Auth.Pass {
			writeHTTPStatus(conn, 407, `Proxy-Authenticate: Basic realm="flarex"`)
			return
		}
		sessionID = extractSession(u, s.Auth.User)
	}

	host, port, err := splitHostPort(req.target)
	if err != nil {
		writeHTTPStatus(conn, 400, "bad target")
		return
	}
	if s.Filter != nil {
		if err := s.Filter.AllowHost(host, port); err != nil {
			writeHTTPStatus(conn, 403, err.Error())
			return
		}
	}

	if s.RateLimit != nil {
		if err := s.RateLimit.Wait(ctx, host); err != nil {
			writeHTTPStatus(conn, 503, "rate limited")
			return
		}
	}

	pol := DialPolicy{
		MaxRetries:  s.MaxRetries,
		BaseBackoff: s.BaseBackoff,
		HedgeAfter:  s.HedgeAfter,
		HMACSecret:  s.HMACSecret,
		Mode:        PickMode(s.GetProxyMode(), host, port),
		SessionID:   sessionID,
		TLSRewrap:   s.TLSRewrap,
	}
	dialStart := time.Now()
	tlsUp := port == 443
	upstream, w, err := DialWithPolicy(ctx, pol, s.Scheduler, s.Pool, host, port, tlsUp)
	metrics.DialLatencyHist.UpdateDuration(dialStart)
	if err != nil {
		metrics.DialFailTotal.Inc()
		writeHTTPStatus(conn, 502, "dial failed")
		return
	}
	metrics.DialSuccessTotal.Inc()
	metrics.WorkerReq(w.Name).Inc()
	w.Inflight.Add(1)
	w.Requests.Add(1)
	defer w.Inflight.Add(-1)
	w.RecordResult(false)
	if s.QuotaHook != nil {
		s.QuotaHook(w.AccountID)
	}
	defer upstream.Close()

	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	logger.L.Debug().Str("req_id", reqID).Str("target", host).Int("port", port).Str("worker", w.Name).Msg("http connect tunnel up")

	relay(conn, upstream)
	metrics.ReqLatencyHist.UpdateDuration(t0)
}

type httpReq struct {
	method string
	target string
}

func readHTTPRequest(br *bufio.Reader) (*httpReq, string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, "", err
	}
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	if len(parts) < 3 {
		return nil, "", fmt.Errorf("malformed request line")
	}
	r := &httpReq{method: parts[0], target: parts[1]}
	var authHdr string
	for {
		h, err := br.ReadString('\n')
		if err != nil {
			return nil, "", err
		}
		if h == "\r\n" || h == "\n" {
			break
		}
		lc := strings.ToLower(h)
		if strings.HasPrefix(lc, "proxy-authorization:") {
			authHdr = strings.TrimSpace(h[len("proxy-authorization:"):])
		}
	}
	return r, authHdr, nil
}

func writeHTTPStatus(conn net.Conn, code int, extra string) {
	msg := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: 0\r\n", code, httpStatusText(code))
	if code == 407 {
		msg += extra + "\r\n"
	}
	msg += "Connection: close\r\n\r\n"
	_, _ = conn.Write([]byte(msg))
}

func httpStatusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 400:
		return "Bad Request"
	case 403:
		return "Forbidden"
	case 405:
		return "Method Not Allowed"
	case 407:
		return "Proxy Authentication Required"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	}
	return "Error"
}

func decodeBasicAuth(h string) (user, pass string, ok bool) {
	if !strings.HasPrefix(strings.ToLower(h), "basic ") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[6:]))
	if err != nil {
		return "", "", false
	}
	i := strings.IndexByte(string(raw), ':')
	if i < 0 {
		return "", "", false
	}
	return string(raw[:i]), string(raw[i+1:]), true
}

func splitHostPort(target string) (string, int, error) {
	if strings.Contains(target, "://") {
		u, err := url.Parse(target)
		if err != nil {
			return "", 0, err
		}
		target = u.Host
	}
	h, p, err := net.SplitHostPort(target)
	if err != nil {
		return "", 0, err
	}
	pn, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, err
	}
	return h, pn, nil
}
