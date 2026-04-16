// Package tlsdial wraps an existing net.Conn with uTLS to impersonate a
// browser TLS fingerprint (JA3). Useful when the proxy itself is the TLS
// client to the target — e.g. when tls_rewrap mode is enabled and the
// upstream client speaks plaintext HTTP over a CONNECT tunnel.
package tlsdial

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"

	utls "github.com/refraction-networking/utls"
)

// profiles is the rotation pool. Mix of recent Chrome, Firefox, Safari, iOS
// fingerprints. Adding more = broader distribution.
var profiles = []utls.ClientHelloID{
	utls.HelloChrome_120,
	utls.HelloChrome_131,
	utls.HelloFirefox_120,
	utls.HelloSafari_16_0,
	utls.HelloIOS_14,
}

// RandomProfile returns a random browser ClientHelloID. Uniform over profiles.
func RandomProfile() utls.ClientHelloID {
	return profiles[rand.IntN(len(profiles))]
}

// Wrap performs a uTLS handshake over an existing net.Conn targeting serverName.
// The returned conn is a *utls.UConn — plaintext reads/writes by the caller,
// encrypted on the wire with the chosen fingerprint.
func Wrap(ctx context.Context, rawConn net.Conn, serverName string, profile utls.ClientHelloID) (net.Conn, error) {
	cfg := &utls.Config{ServerName: serverName}
	u := utls.UClient(rawConn, cfg, profile)
	errCh := make(chan error, 1)
	go func() { errCh <- u.HandshakeContext(ctx) }()
	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("utls handshake: %w", err)
		}
		return u, nil
	case <-ctx.Done():
		_ = u.Close()
		return nil, ctx.Err()
	}
}

// WrapRandom is Wrap with RandomProfile().
func WrapRandom(ctx context.Context, rawConn net.Conn, serverName string) (net.Conn, error) {
	return Wrap(ctx, rawConn, serverName, RandomProfile())
}
