package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

func main() {
	addr := flag.String("addr", ":8787", "listen addr")
	secret := flag.String("secret", os.Getenv("MOCK_HMAC_SECRET"), "HMAC secret (required)")
	window := flag.Int("ts-window", 60, "timestamp validity window (s)")
	flag.Parse()

	if *secret == "" {
		log.Fatal("--secret required (or env MOCK_HMAC_SECRET)")
	}

	h := &handler{secret: *secret, tsWindow: int64(*window)}
	mux := http.NewServeMux()
	mux.HandleFunc("/__health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/", h)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("mockworker listening on %s", *addr)
	log.Fatal(srv.ListenAndServe())
}

type handler struct {
	secret   string
	tsWindow int64
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	host := q.Get("h")
	portS := q.Get("p")
	useTLS := q.Get("t") == "1"
	tsS := q.Get("ts")
	sig := q.Get("s")
	if host == "" || portS == "" || tsS == "" || sig == "" {
		http.Error(w, "missing params", 400)
		return
	}
	port, err := strconv.Atoi(portS)
	if err != nil {
		http.Error(w, "bad port", 400)
		return
	}
	ts, err := strconv.ParseInt(tsS, 10, 64)
	if err != nil {
		http.Error(w, "bad ts", 400)
		return
	}
	now := time.Now().Unix()
	if abs(now-ts) > h.tsWindow {
		http.Error(w, "ts expired", http.StatusUnauthorized)
		return
	}
	if !h.verify(ts, host, port, useTLS, sig) {
		http.Error(w, "bad sig", http.StatusForbidden)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ws.SetReadLimit(-1)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	upstream, err := openUpstream(ctx, host, port, useTLS)
	if err != nil {
		ws.Close(websocket.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer upstream.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer cancel()
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if _, err := upstream.Write(data); err != nil {
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := upstream.Read(buf)
			if n > 0 {
				if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	wg.Wait()
}

func (h *handler) verify(ts int64, host string, port int, useTLS bool, sig string) bool {
	t := 0
	if useTLS {
		t = 1
	}
	msg := fmt.Sprintf("%d|%s|%d|%d", ts, host, port, t)
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write([]byte(msg))
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

func openUpstream(ctx context.Context, host string, port int, useTLS bool) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	if useTLS {
		tc := tls.Client(c, &tls.Config{ServerName: host, InsecureSkipVerify: false})
		if err := tc.HandshakeContext(ctx); err != nil {
			c.Close()
			return nil, err
		}
		return tc, nil
	}
	return c, nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

var _ = io.Copy
