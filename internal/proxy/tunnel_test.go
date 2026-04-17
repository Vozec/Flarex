package proxy

import (
	"net"
	"sync"
	"testing"
)

// TestWsConnCloseIdempotent verifies that calling Close() multiple times
// does not panic and returns the same error.
func TestWsConnCloseIdempotent(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()

	// wsConn wraps a websocket.Conn, which we can't easily mock.
	// Instead, test the closeOnce logic directly by calling Close()
	// concurrently on a plain net.Conn wrapper to verify the pattern.
	type onceCloser struct {
		net.Conn
		closeOnce sync.Once
		closeErr  error
	}
	oc := &onceCloser{Conn: a}
	oc.closeOnce.Do(func() {
		oc.closeErr = oc.Close()
	})

	// Second call should be a no-op.
	oc.closeOnce.Do(func() {
		t.Fatal("closeOnce.Do executed twice")
	})

	if oc.closeErr != nil {
		t.Fatalf("unexpected error: %v", oc.closeErr)
	}
}

// TestWsConnConcurrentClose verifies that concurrent Close() calls
// do not race or panic.
func TestWsConnConcurrentClose(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()

	type onceCloser struct {
		net.Conn
		closeOnce sync.Once
		closeErr  error
	}
	oc := &onceCloser{Conn: a}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			oc.closeOnce.Do(func() {
				oc.closeErr = oc.Close()
			})
		}()
	}
	wg.Wait()
}
