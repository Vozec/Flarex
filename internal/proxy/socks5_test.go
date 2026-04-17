package proxy

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/filter"
)

func pipeConns() (net.Conn, net.Conn) { return net.Pipe() }

func TestHandshakeNoAuth_DomainTarget(t *testing.T) {
	filt, _ := filter.NewIPFilter(nil, []any{443})
	c1, c2 := pipeConns()
	defer c1.Close()
	defer c2.Close()

	errCh := make(chan error, 1)
	reqCh := make(chan *Request, 1)
	go func() {
		r, err := Handshake(c1, nil, filt)
		if err != nil {
			errCh <- err
			return
		}
		reqCh <- r
	}()

	c2.Write([]byte{0x05, 0x01, 0x00})

	buf := make([]byte, 2)
	c2.Read(buf)
	if buf[0] != 5 || buf[1] != 0 {
		t.Fatalf("bad greeting reply: %v", buf)
	}

	dom := "example.com"
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], 443)
	req := make([]byte, 0, 5+len(dom)+len(portBytes))
	req = append(req, 0x05, 0x01, 0x00, 0x03, byte(len(dom)))
	req = append(req, []byte(dom)...)
	req = append(req, portBytes[:]...)
	c2.Write(req)

	select {
	case r := <-reqCh:
		if r.Host != "example.com" || r.Port != 443 {
			t.Errorf("bad request parsed: %+v", r)
		}
		if !r.TLS {
			t.Error("port 443 should infer TLS")
		}
	case err := <-errCh:
		t.Fatalf("handshake err: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandshakeReject_PrivateIP(t *testing.T) {
	filt, _ := filter.NewIPFilter(nil, []any{443})
	c1, c2 := pipeConns()
	defer c1.Close()
	defer c2.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := Handshake(c1, nil, filt)
		errCh <- err
	}()

	c2.Write([]byte{0x05, 0x01, 0x00})
	buf := make([]byte, 2)
	c2.Read(buf)

	req := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x01, 0xbb}
	c2.Write(req)

	reply := make([]byte, 10)
	c2.Read(reply)
	if reply[1] != 0x02 {
		t.Errorf("expected REP=0x02 not_allowed, got 0x%02x", reply[1])
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("should error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandshakeAuthFailed(t *testing.T) {
	filt, _ := filter.NewIPFilter(nil, []any{443})
	auth := &Auth{User: "a", Pass: "b"}
	c1, c2 := pipeConns()
	defer c1.Close()
	defer c2.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := Handshake(c1, auth, filt)
		errCh <- err
	}()

	c2.Write([]byte{0x05, 0x01, 0x02})
	buf := make([]byte, 2)
	c2.Read(buf)

	userpass := []byte{0x01, 0x01, 'x', 0x01, 'x'}
	c2.Write(userpass)

	ar := make([]byte, 2)
	c2.Read(ar)
	if ar[1] == 0x00 {
		t.Error("bad creds should not succeed")
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("should error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandshakeBadVersion(t *testing.T) {
	filt, _ := filter.NewIPFilter(nil, []any{443})
	c1, c2 := pipeConns()
	defer c1.Close()
	defer c2.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := Handshake(c1, nil, filt)
		c1.Close()
		errCh <- err
	}()

	go func() { c2.Write([]byte{0x04, 0x01, 0x00}) }()
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("should error on SOCKS4")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	}
}

var _ = bytes.NewReader
