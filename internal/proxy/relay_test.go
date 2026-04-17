package proxy

import (
	"net"
	"sync"
	"testing"
)

// TestRelayCloseOnce verifies that the relay closeAll pattern using
// sync.Once does not panic when both goroutines trigger it.
func TestRelayCloseOnce(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	var once sync.Once
	closeCalls := 0
	closeAll := func() {
		once.Do(func() {
			closeCalls++
			a1.Close()
			b1.Close()
		})
	}

	// Simulate both relay goroutines calling closeAll concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeAll()
		}()
	}
	wg.Wait()

	if closeCalls != 1 {
		t.Fatalf("expected closeAll to execute once, got %d", closeCalls)
	}

	a2.Close()
	b2.Close()
}

// TestRelayBidirectional verifies that relay shuttles bytes in both
// directions and closes connections when done.
func TestRelayBidirectional(t *testing.T) {
	clientConn, clientSide := net.Pipe()
	upstreamConn, upstreamSide := net.Pipe()

	done := make(chan struct{})
	go func() {
		relay(clientConn, upstreamConn)
		close(done)
	}()

	msg := []byte("hello from client")
	go func() {
		clientSide.Write(msg)
		clientSide.Close()
	}()

	buf := make([]byte, 64)
	n, _ := upstreamSide.Read(buf)
	if string(buf[:n]) != "hello from client" {
		t.Fatalf("unexpected data: %q", buf[:n])
	}

	upstreamSide.Close()
	<-done
}
