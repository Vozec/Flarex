package proxy_test

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/filter"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/proxy"
	"github.com/Vozec/flarex/internal/scheduler"
	"github.com/coder/websocket"
)

const hmacSecret = "testsecret"

func mockWorkerServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__health" {
			w.Write([]byte("ok"))
			return
		}
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

		t := 0
		if tlsF {
			t = 1
		}
		msg := fmt.Sprintf("%d|%s|%d|%d|%s", ts, host, port, t, mode)
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
	}))
	t.Cleanup(srv.Close)
	return srv
}

func targetServer(t *testing.T) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{
		Listener: ln,
		Config:   &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) })},
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestEndToEnd_SOCKS5_ThroughMockWorker(t *testing.T) {
	worker := mockWorkerServer(t)
	target := targetServer(t)

	tu, _ := url.Parse(target.URL)
	targetHost, targetPortS, _ := net.SplitHostPort(tu.Host)
	targetPort, _ := strconv.Atoi(targetPortS)

	w := pool.NewWorker("acc", "mock", worker.URL)
	p := pool.New([]*pool.Worker{w})
	sched := scheduler.NewRoundRobin(p)

	filt, _ := filter.NewIPFilter(nil, []any{targetPort, 80, 443, 8080, 8443})

	_ = filt

	_ = targetHost

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()

	server := &proxy.Server{
		Filter:      filt,
		Scheduler:   sched,
		Pool:        p,
		HMACSecret:  hmacSecret,
		MaxRetries:  1,
		BaseBackoff: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Serve(ctx, socksLn)

	socksAddr := socksLn.Addr().String()
	conn, err := net.DialTimeout("tcp", socksAddr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := socks5Connect(conn, "localhost", targetPort); err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: localhost:%d\r\nConnection: close\r\n\r\n", targetPort)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello") {
		t.Errorf("body = %q, want hello", body)
	}
}

func socks5Connect(c net.Conn, host string, port int) error {

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if buf[0] != 5 || buf[1] != 0 {
		return fmt.Errorf("bad greeting reply %v", buf)
	}

	req := []byte{0x05, 0x01, 0x00}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Is4() {
			req = append(req, 0x01)
			ip4 := a.As4()
			req = append(req, ip4[:]...)
		} else {
			req = append(req, 0x04)
			ip16 := a.As16()
			req = append(req, ip16[:]...)
		}
	} else {
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		return err
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		return err
	}
	if reply[1] != 0 {
		return fmt.Errorf("CONNECT failed rep=0x%02x", reply[1])
	}
	return nil
}
