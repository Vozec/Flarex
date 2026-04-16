[← Back to docs](README.md)

# Proxy modes

FlareX can tunnel your TCP stream through a Worker two ways. The
choice matters because **Cloudflare restricts what Workers can do**
([cloudflare-limitations.md](cloudflare-limitations.md)).

## TL;DR

| Mode | Uses | Works for | Breaks on |
|------|------|-----------|-----------|
| `socket` | `cloudflare:sockets` `connect()` (raw TCP) | Anything (SSH, Redis, TLS, HTTP, ...) | Cloudflare-hosted destinations (CF blocks CF→CF connect) |
| `fetch` | Worker `fetch()` API | Plain HTTP/1.x + HTTP/2 | Non-HTTP (TLS, SSH), would be corrupted |
| `hybrid` | socket first, byte-sniff, fetch only if HTTP | All combinations above, safely | none |

**Default:** `hybrid`. Don't change unless you know why.

## Socket mode

The Worker opens a raw TCP socket to the target with
[`cloudflare:sockets`](https://developers.cloudflare.com/workers/runtime-apis/sockets/),
then bidirectionally relays bytes. FlareX wraps this in a WebSocket tunnel
from client → proxy → Worker, with a 1-byte probe after handshake
(`0x01`) so we detect early connect failures.

**Pros:** protocol-agnostic. SSH, IMAP, PostgreSQL, MySQL, raw TCP all
pass through unchanged. TLS is end-to-end between your client and the target.

**Cons:** CF forbids `connect()` to their own IP ranges (anti-loop). If your
target resolves to a CF-hosted IP (a lot of websites do), the Worker closes
with code **`4001`** = `ErrUpstreamBlocked`.

## Fetch mode

The Worker reads your HTTP request off the tunnel and re-issues it via
`fetch()`. CF edge adds a small latency penalty (~200-500ms cold) and
handles TLS at the edge on your behalf.

**Pros:** works on CF-hosted targets because `fetch()` uses the CF internal
routing, not a raw TCP connect.

**Cons:**
- **HTTP-only.** Non-HTTP (TLS, SSH) streams are fed to the Worker's HTTP
  parser. It'll dumb-parse the bytes and drop the connection, or worse,
  respond with an HTTP error that your non-HTTP client won't understand.
- The Worker rewrites your `Host` header, so vhost tricks can be blocked.
- TLS is terminated at the CF edge. **You trust CF with your TLS content**
  for that leg.

## Hybrid mode (the interesting one)

Default algorithm:

1. Pick a Worker. Dial in **socket** mode.
2. Socket succeeds: fast path, raw TCP relay. Done.
3. Socket fails with `ErrUpstreamBlocked` (CF-hosted target): the server
   replies "CONNECT OK" to the client anyway (optimistic) and **peeks the
   first 256 bytes** from the client (1-second window).
4. Run `LooksLikeHTTP(peek)`. Matches HTTP/1.x request lines
   (`GET /`, `POST /`, `CONNECT host`, ...) + HTTP/2 connection preface.
5. **HTTP?** Promote to fetch mode, re-dial, replay the peeked bytes into the
   new tunnel, then relay normally.
6. **Non-HTTP?** (TLS ClientHello `0x16 0x03 ...`, SSH banner, random TCP): close.

Port-based heuristics ("port 80 = fetch, port 443 = socket") are **wrong**
in practice. Any port can carry anything:

- Port 443 can carry HTTP/3, QUIC, a TLS-wrapped IRC, ...
- Port 80 can carry a plain IRC or Redis serving unencrypted.
- Port 22 is SSH today, but a reverse-proxy may have it serving HTTP.

Byte-sniff is the only reliable way.

### What "looks like HTTP"?

See `internal/proxy/sniff.go`:

- HTTP/1.x methods: `GET `, `POST `, `HEAD `, `PUT `, `DELETE `, `OPTIONS `,
  `PATCH `, `TRACE `, `CONNECT ` (note trailing space, matches RFC 7231).
- HTTP/2 client preface: `PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n`.

**False positive risk:** a payload that *literally* starts with `GET ` (e.g.
a weird binary protocol) would be promoted to fetch and corrupted. Nothing
real does this in practice; the check is conservative.

**False negative risk:** a streaming HTTP client that sends a single byte
and waits is stalled for 1s, then gets closed. The 1s timeout catches this.
Use `socks5h://` (DNS through proxy) so the Worker resolves the target.
The hostname often has a non-CF A record and socket works.

## The "known unreachable" cache

After a `4001` on `host:port`, we remember it in `cfUnreachable` (TTL
`timeouts.cf_blocked_cache_ttl`, default 10m).

**In hybrid mode by default:** the cache is *informational only*. Every
dial still tries socket first, so we detect recovery. Setting
`DialPolicy.AutoFetchFallback=true` (internal, not exposed yet) uses the
cache to pre-emptively start in fetch. Unsafe without byte-sniff
(non-HTTP targets get corrupted), so it's off.

## Live examples

See [recipes.md §4](recipes.md#4-traffic-types) for verified end-to-end
outputs:
- [4.1 HTTPS to non-CF target (socket path)](recipes.md#41-https-to-a-non-cloudflare-target-socket-path)
- [4.6 socks5h vs socks5, CF vs non-CF](recipes.md#46-socks5h-vs-socks5-dns-at-proxy-vs-at-client)
- [2.6 force fetch-only mode](recipes.md#26-force-fetch-only-proxy-mode)
- [2.7 hybrid default](recipes.md#27-hybrid-proxy-mode-default-smart-fallback)

## Choosing the right mode

Rules of thumb:

| You want to proxy… | Use |
|---------------------|-----|
| Normal mixed traffic (HTTPS to arbitrary websites) | `hybrid` |
| Only HTTP(S) to CF-hosted APIs (Cloudflare dashboard, many SaaS) | `fetch` |
| Only raw TCP (SSH, IRC, Redis, PostgreSQL), target never CF-hosted | `socket` (slightly faster, skips sniff overhead) |
| Maximum throughput, happy to drop CF-hosted targets | `socket` + `disable_probe: true` |

## Forcing a mode from the CLI

```bash
flarex server --proxy-mode socket
```

Overrides `pool.proxy_mode` for that run.

## See also

- [cloudflare-limitations.md](cloudflare-limitations.md) : why CF blocks CF.
- [tls-rewrap.md](tls-rewrap.md) : orthogonal feature for JA3 evasion.
- [troubleshooting.md](troubleshooting.md) : "Connection reset" / hangs.
