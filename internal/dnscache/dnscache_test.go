package dnscache

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDialContextHitsCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	c := New(time.Minute)
	if _, err := c.lookup(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	conn, err := c.DialContext(context.Background(), "tcp", net.JoinHostPort(host, port))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	if got := c.Stats()[host]; got == 0 {
		t.Errorf("stats=%v", c.Stats())
	}
}

func TestWarmParallel(t *testing.T) {
	c := New(time.Minute)
	c.Warm(context.Background(), []string{"localhost", "127.0.0.1"})
	if len(c.Stats()) == 0 {
		t.Error("nothing cached")
	}
}
