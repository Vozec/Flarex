package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"

	"github.com/Vozec/flarex/internal/filter"
	"github.com/Vozec/flarex/internal/metrics"
)

const (
	socksVer5 byte = 0x05

	methodNoAuth   byte = 0x00
	methodUserPass byte = 0x02
	methodReject   byte = 0xFF

	cmdConnect byte = 0x01

	atypIPv4   byte = 0x01
	atypDomain byte = 0x03
	atypIPv6   byte = 0x04

	repSuccess        byte = 0x00
	repGeneralFailure byte = 0x01
	repNotAllowed     byte = 0x02
	repNetUnreachable byte = 0x03
	repHostUnreach    byte = 0x04
	repConnRefused    byte = 0x05
	repTTLExpired     byte = 0x06
	repCmdUnsupported byte = 0x07
	repATypUnsupport  byte = 0x08
)

type Auth struct {
	User string
	Pass string
}

type Request struct {
	Host    string
	Port    int
	TLS     bool
	Session string
}

func Handshake(conn net.Conn, auth *Auth, filt *filter.IPFilter) (*Request, error) {
	br := bufReader(conn)

	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, fmt.Errorf("greeting: %w", err)
	}
	if header[0] != socksVer5 {
		return nil, fmt.Errorf("bad socks version %d", header[0])
	}
	nmethods := int(header[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(br, methods); err != nil {
		return nil, err
	}

	want := methodNoAuth
	if auth != nil {
		want = methodUserPass
	}
	ok := false
	for _, m := range methods {
		if m == want {
			ok = true
			break
		}
	}
	if !ok {
		_, _ = conn.Write([]byte{socksVer5, methodReject})
		return nil, fmt.Errorf("no acceptable auth method")
	}
	if _, err := conn.Write([]byte{socksVer5, want}); err != nil {
		return nil, err
	}

	var session string
	if auth != nil {
		u, err := userPassAuth(br, conn, auth)
		if err != nil {
			return nil, err
		}
		session = extractSession(u, auth.User)
	}

	reqHead := make([]byte, 4)
	if _, err := io.ReadFull(br, reqHead); err != nil {
		return nil, err
	}
	if reqHead[0] != socksVer5 {
		return nil, fmt.Errorf("bad socks version in request")
	}
	if reqHead[1] != cmdConnect {
		reply(conn, repCmdUnsupported)
		return nil, fmt.Errorf("only CONNECT supported")
	}

	var host string
	switch reqHead[3] {
	case atypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		a, _ := netip.AddrFromSlice(buf)
		host = a.String()
	case atypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		a, _ := netip.AddrFromSlice(buf)
		host = a.String()
	case atypDomain:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(br, lb); err != nil {
			return nil, err
		}
		dom := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(br, dom); err != nil {
			return nil, err
		}
		host = string(dom)
	default:
		reply(conn, repATypUnsupport)
		return nil, fmt.Errorf("unsupported atyp %d", reqHead[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return nil, err
	}
	port := int(binary.BigEndian.Uint16(portBuf))

	if err := filt.AllowHost(host, port); err != nil {
		reply(conn, repNotAllowed)
		metrics.FilterDenied.Inc()
		return nil, fmt.Errorf("filter deny: %w", err)
	}

	return &Request{Host: host, Port: port, TLS: port == 443 || port == 8443, Session: session}, nil
}

// extractSession parses a sticky-session suffix from an authenticated SOCKS5
// username. Supported forms, case-sensitive:
//   - "<user>-session-<id>"   (luminati / brightdata convention)
//   - "<user>:session:<id>"   (alternate)
//
// Returns "" when the username is the bare configured user.
func extractSession(user, base string) string {
	if user == base {
		return ""
	}
	for _, sep := range []string{"-session-", ":session:"} {
		if p := base + sep; len(user) > len(p) && user[:len(p)] == p {
			return user[len(p):]
		}
	}
	return ""
}

func ReplySuccess(conn net.Conn) error {
	return reply(conn, repSuccess)
}

func ReplyFail(conn net.Conn, code byte) { _ = reply(conn, code) }

func reply(conn net.Conn, code byte) error {

	buf := []byte{socksVer5, code, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(buf)
	return err
}

func userPassAuth(r io.Reader, w io.Writer, auth *Auth) (string, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		return "", err
	}
	if head[0] != 0x01 {
		return "", fmt.Errorf("bad userpass version")
	}
	uLen := int(head[1])
	user := make([]byte, uLen)
	if _, err := io.ReadFull(r, user); err != nil {
		return "", err
	}
	pLenBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, pLenBuf); err != nil {
		return "", err
	}
	pass := make([]byte, int(pLenBuf[0]))
	if _, err := io.ReadFull(r, pass); err != nil {
		return "", err
	}
	u := string(user)
	// Accept exact match OR "user-session-<id>" / "user:session:<id>" variants.
	if !authMatches(u, auth.User) || string(pass) != auth.Pass {
		w.Write([]byte{0x01, 0x01})
		return "", errors.New("bad credentials")
	}
	if _, err := w.Write([]byte{0x01, 0x00}); err != nil {
		return "", err
	}
	return u, nil
}

func authMatches(user, base string) bool {
	if user == base {
		return true
	}
	for _, sep := range []string{"-session-", ":session:"} {
		p := base + sep
		if len(user) > len(p) && user[:len(p)] == p {
			return true
		}
	}
	return false
}

func bufReader(c net.Conn) io.Reader { return c }

var _ = strconv.Itoa

func Listen(ctx context.Context, addr string, unixPerms uint32) (net.Listener, error) {
	scheme, real := splitScheme(addr)
	switch scheme {
	case "tcp":
		lc := net.ListenConfig{Control: tcpListenerControl}
		return lc.Listen(ctx, "tcp", real)
	case "unix":
		_ = removeIfExists(real)
		lc := net.ListenConfig{}
		l, err := lc.Listen(ctx, "unix", real)
		if err != nil {
			return nil, err
		}
		_ = chmod(real, unixPerms)
		return l, nil
	default:
		return nil, fmt.Errorf("bad listen addr %q (use tcp://host:port or unix:///path)", addr)
	}
}

func splitScheme(s string) (scheme, rest string) {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == ':' && s[i+1] == '/' && s[i+2] == '/' {
			return s[:i], s[i+3:]
		}
	}
	return "tcp", s
}
