package proxy

import (
	"net"
	"testing"
	"time"
)

// TestDrainResChanClosesConns verifies that drainResChan properly
// closes connections left in the channel buffer.
func TestDrainResChanClosesConns(t *testing.T) {
	ch := make(chan dialRes, 2)

	a, b := net.Pipe()
	defer b.Close()

	ch <- dialRes{conn: a, err: nil, worker: nil}

	drainResChan(ch)

	// a should be closed by drain; writing should fail.
	_, err := a.Write([]byte("x"))
	if err == nil {
		t.Fatal("expected write to closed conn to fail")
	}
}

// TestDrainResChanEmpty verifies that drainResChan returns immediately
// on an empty channel.
func TestDrainResChanEmpty(t *testing.T) {
	ch := make(chan dialRes, 2)

	start := time.Now()
	drainResChan(ch)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("drainResChan should return immediately on empty channel, took %v", elapsed)
	}
}

// TestDrainResChanNilConn verifies that nil connections don't panic.
func TestDrainResChanNilConn(t *testing.T) {
	ch := make(chan dialRes, 2)
	ch <- dialRes{conn: nil, err: nil, worker: nil}

	// Should not panic.
	drainResChan(ch)
}

// TestDrainResChanMultiple verifies that all buffered results are drained.
func TestDrainResChanMultiple(t *testing.T) {
	ch := make(chan dialRes, 2)

	a1, b1 := net.Pipe()
	a2, b2 := net.Pipe()
	defer b1.Close()
	defer b2.Close()

	ch <- dialRes{conn: a1, err: nil, worker: nil}
	ch <- dialRes{conn: a2, err: nil, worker: nil}

	drainResChan(ch)

	// Both should be closed.
	if _, err := a1.Write([]byte("x")); err == nil {
		t.Fatal("expected a1 to be closed")
	}
	if _, err := a2.Write([]byte("x")); err == nil {
		t.Fatal("expected a2 to be closed")
	}
}
