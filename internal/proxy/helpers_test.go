package proxy_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

func spinMockWorker(tb testing.TB) *testServer {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/__health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		host := q.Get("h")
		portS := q.Get("p")
		tlsF := q.Get("t") == "1"
		tsS := q.Get("ts")
		sig := q.Get("s")
		mode := q.Get("m")
		if mode == "" {
			mode = "socket"
		}
		ts, _ := strconv.ParseInt(tsS, 10, 64)
		port, _ := strconv.Atoi(portS)

		tt := 0
		if tlsF {
			tt = 1
		}
		msg := fmt.Sprintf("%d|%s|%d|%d|%s", ts, host, port, tt, mode)
		mac := hmac.New(sha256.New, []byte(hmacSecret))
		mac.Write([]byte(msg))
		want := hex.EncodeToString(mac.Sum(nil))
		if want != sig {
			http.Error(w, "bad sig", http.StatusForbidden)
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		ws.SetReadLimit(-1)
		ws.Write(r.Context(), websocket.MessageBinary, []byte{0x01})
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		c, err := net.Dial("tcp", net.JoinHostPort(host, portS))
		if err != nil {
			ws.Close(websocket.StatusBadGateway, "upstream")
			return
		}
		defer c.Close()
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
				if _, err := c.Write(data); err != nil {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			defer cancel()
			buf := make([]byte, 32*1024)
			for {
				n, err := c.Read(buf)
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
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	tb.Cleanup(func() { srv.Close(); ln.Close() })
	return &testServer{URL: "http://" + ln.Addr().String(), ln: ln, srv: srv}
}

func spinTarget(tb testing.TB) *testServer {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) })}
	go srv.Serve(ln)
	tb.Cleanup(func() { srv.Close(); ln.Close() })
	return &testServer{URL: "http://" + ln.Addr().String(), ln: ln, srv: srv}
}
