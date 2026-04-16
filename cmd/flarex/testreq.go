package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Vozec/flarex/internal/admin"
	"github.com/Vozec/flarex/internal/proxy"
)

// ipRegex captures the first plausible public IPv4 or IPv6 literal anywhere
// in a response body. Used to sniff out "what's my IP" endpoints when
// response headers don't expose the egress (most non-CF targets).
var ipRegex = regexp.MustCompile(
	`(?:[0-9]{1,3}\.){3}[0-9]{1,3}|(?:[A-Fa-f0-9]{1,4}:){2,7}[A-Fa-f0-9]{1,4}`,
)

const testRequestBodyCap = 4096

// runTestRequest drives a single HTTP GET through the proxy's current
// dial path (same workers, same policy) and returns a structured report
// for the admin UI. Callers pass a context with an upper-bound deadline.
func runTestRequest(ctx context.Context, srv *proxy.Server, targetURL string) (admin.TestRequestResult, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return admin.TestRequestResult{}, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return admin.TestRequestResult{}, fmt.Errorf("scheme must be http or https")
	}
	if u.Host == "" {
		return admin.TestRequestResult{}, fmt.Errorf("missing host")
	}

	port := 80
	tlsUp := u.Scheme == "https"
	if tlsUp {
		port = 443
	}
	host := u.Hostname()
	if p := u.Port(); p != "" {
		if pi, err := strconv.Atoi(p); err == nil {
			port = pi
		}
	}

	pol := proxy.DialPolicy{
		MaxRetries:  srv.MaxRetries,
		BaseBackoff: srv.BaseBackoff,
		HedgeAfter:  srv.HedgeAfter,
		HMACSecret:  srv.HMACSecret,
		Mode:        proxy.PickMode(srv.GetProxyMode(), host, port),
		TLSRewrap:   srv.TLSRewrap,
	}

	start := time.Now()
	upstream, w, err := proxy.DialWithPolicy(ctx, pol, srv.Scheduler, srv.Pool, host, port, tlsUp)
	// CF-hosted targets 4001 on socket mode. Unlike the SOCKS5 flow (which
	// byte-sniffs the client's first packet), test-request is server-
	// originated — we know it's an HTTP request, so auto-promote to fetch.
	// The Worker's fetch() API handles HTTPS natively (TLS terminates at
	// the edge), so it's safe regardless of scheme.
	if errors.Is(err, proxy.ErrUpstreamBlocked) && pol.Mode != proxy.ModeFetch {
		pol.Mode = proxy.ModeFetch
		upstream, w, err = proxy.DialWithPolicy(ctx, pol, srv.Scheduler, srv.Pool, host, port, tlsUp)
	}
	if err != nil {
		return admin.TestRequestResult{}, fmt.Errorf("dial: %w", err)
	}
	defer upstream.Close()

	// Wrap with TLS client-side since the Worker tunnel is plaintext above
	// the WS; the target still expects a TLS handshake. For mode=fetch the
	// Worker terminates TLS at the edge so the stream we have is already
	// plaintext HTTP — don't double-wrap.
	reader := bufio.NewReader(upstream)
	writer := io.Writer(upstream)
	if tlsUp && pol.Mode == proxy.ModeSocket {
		tconn := tls.Client(upstream, &tls.Config{ServerName: host})
		if herr := tconn.HandshakeContext(ctx); herr != nil {
			return admin.TestRequestResult{}, fmt.Errorf("tls handshake: %w", herr)
		}
		reader = bufio.NewReader(tconn)
		writer = tconn
		defer tconn.Close()
	}

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(writer, "GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: flarex-test-request/1\r\nAccept: */*\r\nConnection: close\r\n\r\n", path, u.Host)

	resp, rerr := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if rerr != nil {
		return admin.TestRequestResult{}, fmt.Errorf("read response: %w", rerr)
	}
	defer resp.Body.Close()

	// Cap body to keep the payload small in the admin UI.
	bodyBuf := &bytes.Buffer{}
	_, _ = io.CopyN(bodyBuf, resp.Body, testRequestBodyCap+1)
	body := bodyBuf.Bytes()
	truncAt := 0
	if len(body) > testRequestBodyCap {
		body = body[:testRequestBodyCap]
		truncAt = testRequestBodyCap
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		headers[k] = strings.Join(v, ", ")
	}

	res := admin.TestRequestResult{
		Worker:       w.Name,
		Colo:         w.ColoString(),
		Status:       resp.StatusCode,
		LatencyMs:    time.Since(start).Milliseconds(),
		Mode:         pol.Mode,
		Headers:      headers,
		Body:         string(body),
		BodyTruncAt:  truncAt,
		ResolvedHost: host,
	}
	// Egress IP heuristics. Priority:
	//   1. Cf-Connecting-IP / X-Forwarded-For response header.
	//   2. Parse body for known JSON shapes (ipify, httpbin, tpapi).
	//   3. cf/trace key=value (Cloudflare's own echo).
	//   4. Fallback: first IP-shaped substring in body.
	if ip := headers["Cf-Connecting-Ip"]; ip != "" {
		res.EgressIP = ip
	} else if ip := headers["X-Forwarded-For"]; ip != "" {
		res.EgressIP = strings.Split(ip, ",")[0]
	} else if ip := sniffBodyIP(res.Body); ip != "" {
		res.EgressIP = ip
	}
	return res, nil
}

// sniffBodyIP extracts an IP from the common "what's my IP" response
// shapes. Best effort — returns "" if nothing looks plausible.
func sniffBodyIP(body string) string {
	trim := strings.TrimSpace(body)
	// httpbin: {"origin": "1.2.3.4"} / ipify: {"ip":"1.2.3.4"}
	if strings.HasPrefix(trim, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(trim), &obj); err == nil {
			for _, k := range []string{"ip", "origin", "address", "clientIp", "client_ip", "yourIp"} {
				if v, ok := obj[k]; ok {
					if s, ok := v.(string); ok {
						if ip := extractIP(s); ip != "" {
							return ip
						}
					}
				}
			}
		}
	}
	// cloudflare.com/cdn-cgi/trace: key=value lines, including `ip=...`.
	for _, line := range strings.Split(trim, "\n") {
		if eq := strings.IndexByte(line, '='); eq > 0 {
			if strings.EqualFold(strings.TrimSpace(line[:eq]), "ip") {
				if ip := extractIP(strings.TrimSpace(line[eq+1:])); ip != "" {
					return ip
				}
			}
		}
	}
	// Plain ifconfig.me-style response — body is just the IP.
	if ip := extractIP(trim); ip != "" {
		return ip
	}
	return ""
}

func extractIP(s string) string {
	m := ipRegex.FindString(s)
	if m == "" {
		return ""
	}
	if ip := net.ParseIP(m); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		return ip.String()
	}
	return ""
}
