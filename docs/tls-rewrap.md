[← Back to docs](README.md)

# uTLS rewrap (JA3 rotation)

Opt-in feature that makes the TLS fingerprint of your outbound connections
rotate across a pool of popular browser ClientHello signatures. Useful when
the target fingerprint-locks clients (CDN bot protections, anti-scraping).

**Config knob:** `pool.tls_rewrap: true`.

## What's a JA3?

[JA3](https://engineering.salesforce.com/tls-fingerprinting-with-ja3-and-ja3s-97bd22b30a1f/)
is a hash of the TLS ClientHello (version + cipher suites + extensions +
curves + point formats). Different stacks (Go net/http, curl with OpenSSL,
Chrome with BoringSSL, Firefox with NSS, Safari) produce different
ClientHellos and therefore different JA3s.

Cloudflare, Akamai, DataDome, PerimeterX all use JA3 (or the newer JA4) to
spot "that client is probably a Python scraper, not a browser".

## What FlareX does

When `tls_rewrap: true` and the destination is TLS (port 443, `req.TLS=true`
from SOCKS5), FlareX wraps the Worker tunnel with a
[uTLS](https://github.com/refraction-networking/utls) client that
**impersonates a real browser's ClientHello**, picked randomly per
connection from:

- `Chrome_120`
- `Chrome_131`
- `Firefox_120`
- `Safari_16_0`
- `iOS_14`

The target sees a TLS handshake that matches the chosen browser's JA3 byte
for byte.

## Important semantic change

When `tls_rewrap` is enabled, **FlareX becomes the TLS client** to the
target. Consequence: the client that dialed through the SOCKS5 proxy must
speak **plaintext** over the tunnel. FlareX handles TLS, not the client.

**This breaks standard HTTPS-through-SOCKS5 flows** where the client expects
to do TLS itself inside the tunnel:

```
┌── without tls_rewrap ──┐          ┌── with tls_rewrap ──┐
 curl ─TLS─▶ flarex              curl ─plain─▶ flarex
       (opaque bytes)                          │
       relay              ──▶                  ▼
                                      FlareX does TLS with
                                      random JA3 → target
```

### When is `tls_rewrap` useful then?

When you control both sides: a custom scanner that speaks plaintext over
CONNECT and wants the proxy to do the TLS. Or when you're piping through
FlareX from a tool that doesn't do TLS itself and just gives you a TCP
socket.

### When not to turn it on

- **Standard `curl -x socks5://...`**: curl does TLS itself; it would fail
  because your client's ClientHello goes through the tunnel but the tunnel's
  other end terminates TLS. Net result: handshake mismatch, nothing works.

## The current limitation

Right now `tls_rewrap` is only meaningful at the `socket` level. The feature
is documented and plumbed; in practice you need a client that speaks
plaintext to `127.0.0.1:1080` and expects plaintext back. A natural
extension ("FlareX terminates TLS and gives the client the plaintext
body") requires a different frontend API (custom HTTP-over-CONNECT), not
yet implemented.

**TL;DR: this is a power-user flag.** For 99% of use cases leave
`tls_rewrap: false`. Rotating Worker IPs + byte-level protocol
transparency already give you most of the evasion benefit.

## Under the hood

Code: `internal/tlsdial/utls.go`.

```go
// Random profile pick
profile := utls.HelloChrome_131
// Wrap an existing net.Conn (the Worker WS tunnel)
conn, err := tlsdial.WrapRandom(ctx, rawConn, targetHost)
// conn is now a *utls.UConn; reads/writes are plaintext for the caller,
// encrypted on the wire with Chrome_131 JA3.
```

Handshake deadline is `tls_handshake` timeout (default 10s).

## Extending the pool

Add more profiles in `internal/tlsdial/utls.go`:

```go
var profiles = []utls.ClientHelloID{
    utls.HelloChrome_120,
    utls.HelloChrome_131,
    utls.HelloFirefox_120,
    utls.HelloSafari_16_0,
    utls.HelloIOS_14,
    // add more from uTLS lib
}
```

Library ships with ~30 profiles; pick recent versions for realism.

## Testing your JA3

Point FlareX at a JA3-revealing service:

```bash
curl -x socks5h://127.0.0.1:1080 https://tls.peet.ws/api/all
# Look at ja3_hash and peetprint in the response.
```

With `tls_rewrap: true`, you should see a Chrome/Firefox/Safari JA3 that
varies across runs.
