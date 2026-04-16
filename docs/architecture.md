[← Back to docs](README.md)

# Architecture

How FlareX routes a single connection, and why the code is shaped the way it is.

## High-level path

```
client  ─[1]→  SOCKS5 / HTTP CONNECT handshake
               │
           [2] pick worker (scheduler or session hash)
               │
           [3] dial worker through WebSocket + HMAC
               │  ┌ probe 0x01 or error
               │  │
           [4] relay bytes bi-directional (io.CopyBuffer + sync.Pool bufs)
               │
           [5] close → record metrics + circuit breaker signal
```

Every numbered step is a separate concern in the code; they're wired in
`internal/proxy/server.go:handle()`.

## Key data structures

### Pool snapshot (`internal/pool/pool.go`)

```go
type Pool struct {
    snap   atomic.Pointer[[]*Worker]
    quotas map[string]*Quota
    rr     atomic.Uint64
    mu     sync.Mutex
}
```

- `snap` is an **atomic pointer to a slice**. Readers on the hot path
  (`NextRR`, `NextSkip`, `ByAccount`) deref without locking. Writers
  (`Add`, `Remove`, `Replace`) take `mu`, build a new slice, then `Store`
  it.
- Cost of a pool read: **one atomic load, one bounds-check, no allocs**.
  Benchmark: 47 ns/op.
- Cost of a pool mutation: O(n) copy, but mutations happen at
  deploy/rotate rate, not request rate.

### Worker (`internal/pool/pool.go`)

Per-worker atomics:
- `Inflight atomic.Int64` : concurrent tunnels.
- `Requests atomic.Uint64` : cumulative count.
- `Errors atomic.Uint64` : cumulative errors.
- `Healthy atomic.Bool` : gate for `Available()`.
- `QuotaPaused atomic.Bool` : gate for quota auto-pause.
- `EWMAErr atomic.Uint64` (float64 bits) : exponentially-weighted moving
  average of error rate, updated on each request.

### HTTP client per Worker

Each `Worker` lazily owns a tuned `http.Client` (`HTTPClient()`):

```go
MaxIdleConns:        200
MaxIdleConnsPerHost: 100
IdleConnTimeout:     120s
ForceAttemptHTTP2:   true
ReadBufferSize:      256 KB
WriteBufferSize:     256 KB
HTTP/2 tuning:
  MaxReadFrameSize:           1 MB
  ReadIdleTimeout:            30s
  PingTimeout:                10s
  StrictMaxConcurrentStreams: false
```

Reusing this client across requests means the TCP+TLS handshake happens
**once per Worker**. Every subsequent WebSocket upgrade rides an existing
HTTP/2 stream on the same TLS conn. Dial latency post-warmup is ~5-20ms
locally instead of ~50-100ms.

## Request lifecycle

### 1. Handshake (`internal/proxy/socks5.go` / `httpconnect.go`)

- SOCKS5: parse greeting, negotiate auth, CONNECT request, extract host /
  port / session.
- HTTP CONNECT: parse request line + headers, extract target, auth, reply
  200 on success.

Both feed a uniform `Request` struct.

### 2. Worker selection (`internal/pool/pool.go` + `internal/scheduler`)

- `SessionID` set: `NextBySession(session)` = `FNV(session) mod N`.
  Skips unavailable workers.
- No session: `Scheduler.Next()`. Current impl is `round_robin` via
  `atomic.Uint64` counter modulo pool size.

Breaker state counted in `Available()`: unhealthy or open-breaker Workers
are skipped. If everyone is unavailable after retries, the dial fails.

### 3. Dial + probe (`internal/proxy/tunnel.go` + `dial.go`)

```
DialWorker(ctx, w, hmacSecret, host, port, tls, mode)
  │
  ├─ auth.Sign(secret, host, port, tls, mode) → timestamp, signature
  │
  ├─ url = ws://worker/?h=..&p=..&t=..&ts=..&s=..&m=..
  │
  ├─ websocket.Dial(url, httpClient)   ← reuses HTTP/2 conn pool
  │
  ├─ if probe enabled:
  │     read 1 byte with short deadline
  │     0x01 = OK, anything else = error (probably 4001, ErrUpstreamBlocked)
  │
  └─ return wsConn{ws: ...}    ← net.Conn adapter over WebSocket
```

The `wsConn` adapter (`internal/proxy/tunnel.go`) implements `net.Conn` on
top of a `coder/websocket.Conn` by buffering reader frames and wrapping
writes in binary messages. `SetReadLimit(-1)` disables the 32KB default
cap.

### 4. Retry + hedge (`internal/proxy/dial.go`)

```go
for attempt := 0 ; attempt < MaxRetries ; attempt++ {
    pick worker (session or RR, skipping tried)
    dial  ← may hedge: if slow, fire a 2nd worker after HedgeAfter ms
    if ErrUpstreamBlocked && !AutoFetchFallback:
        return ErrUpstreamBlocked   ← server layer decides via byte-sniff
    on other error: record breaker failure, exp backoff, retry
}
```

Hedging uses `dialOnceWithHedge`: when the primary dial is still pending
after `HedgeAfter` ms, fire a second dial on a fresh Worker. First success
wins; loser is context-canceled and its conn closed. Kills long-tail
latency from one slow Worker.

### 5. Byte-sniff fallback (`internal/proxy/sniff.go` + `server.go`)

On `ErrUpstreamBlocked` in hybrid mode:

```
server replies "CONNECT OK" to client
peek up to 256 bytes with 1s deadline
  │
  ├─ LooksLikeHTTP(peek)?
  │     yes : redial in fetch mode, write peek bytes, relay
  │     no  : close (can't fetch non-HTTP)
  │
  └─ on read timeout (client didn't send) : close
```

See [proxy-modes.md](proxy-modes.md).

### 6. Circuit breaker (`internal/pool/pool.go`)

Library: `sony/gobreaker`. Per-Worker instance. Trips open if:

- 5+ consecutive failures, OR
- 10+ requests with >50% failure rate (over the `breaker_interval` window,
  default 60s).

Open state = Worker treated as unhealthy for `breaker_open` (default 30s).
Then half-open: next request is a canary; success closes, failure
re-opens.

Caps damage when a Worker account gets suspended or a CF region outage hits.

### 7. Rate limiting (`internal/ratelimit`)

Per-target-host token bucket (`golang.org/x/time/rate`). Controlled by
`rate_limit.per_host_qps` and `rate_limit.per_host_burst`. Blocks the dial
goroutine at the top of `server.handle`.

Cheap: 1 map lookup per request (`sync.Map`), 1 `Reserve()` call.

### 8. Graceful drain (`internal/worker/rotator.go`)

When the rotator recycles a Worker:

1. Deploy replacement.
2. Atomically swap in the pool (`Replace`).
3. Mark old Worker `Healthy=false` so new dials skip it.
4. **Drain**: poll `old.Inflight.Load() == 0` every `drain_poll` (100ms) up
   to `drain_timeout` (30s). If zero, delete. If timeout, force delete
   (connections were too long; they'll see an abrupt close).
5. Delete from CF.
6. Update state DB.

Guarantees **zero interrupted requests** for tunnels that finish within
30s of rotation trigger. Long-lived connections (web sockets, shell
sessions) beyond that get reset. Document this to your users.

## Why certain choices

### Why WebSocket instead of raw HTTP/2?

A Worker's `fetch()` handler only gets one request + one response. Tunneling
bi-directional arbitrary TCP through it needs a long-lived channel. WebSocket
keeps the Worker's fetch handler alive, doesn't buffer messages, and is a
first-class feature on CF Workers.

### Why one HTTP/2 client per Worker (not a shared one)?

Sharing would mean a single `http.Transport` does connection pooling for
every Worker. When workers rotate, the pool retains dead conns; HTTP/2
stream caps mix traffic between workers. Per-Worker clients give isolation
and natural GC.

### Why atomic snapshot for the pool?

Because `NextRR` is called **once per incoming client connection**, which
at peak is tens of thousands per second. `sync.Mutex`/`RWMutex` adds
20-50ns. Atomic deref adds ~1ns. At 100k conn/s that's 2-5ms of CPU saved.

### Why not pre-open idle WS tunnels?

A true pre-connect pool would keep N WebSocket tunnels open to each Worker
at all times, handing an already-open tunnel to the next client. We don't
do it because:

- **WS upgrade cost is small** on a warm HTTP/2 connection. A new WS is
  just a new HTTP/2 stream, ~5-10 ms locally.
- **Target `connect()` dominates dial latency** (30-150 ms for the
  Worker→target TCP handshake). Saving the WS step doesn't move the
  needle.
- **Tunnels are protocol-bound** at handshake time: each WS URL carries
  `h=`, `p=`, `m=`, HMAC in query params. Pre-opening would require a
  post-handshake re-routing protocol on the Worker (invasive and fragile).

What we DO pre-connect is the underlying HTTP/2 transport to every Worker
(`KeepAlive` loop in `internal/proxy/prewarm.go`). That amortizes the TLS
+ TCP handshake cost across all subsequent dials. First dial on a fresh
Worker can cost ~80-150 ms; subsequent dials ~5-20 ms.

### Why the 1-byte probe?

Without a probe, we'd only know the dial failed when the first real byte
fails to round-trip, too late (client already sent data). The probe is a
cheap "are you really connected?" post-handshake check that lets us bail
early with `ErrUpstreamBlocked` and run the byte-sniff fallback. Cost: 1
byte + 800ms worst-case. `disable_probe: true` removes it for throughput.

### Why zerolog and not slog?

Drop-in replacement for `phuslu/log` which we initially picked for
performance. `slog` landed in Go 1.21 but has a verbose API (attrs are
`slog.Any("key", v)` everywhere). `zerolog`'s chainable `.Str().Int().Msg()`
API is terser and was ~1.5x faster in our benchmarks at time of writing.

### Why FlareX and not a real VPN (WireGuard, Tailscale, ...)?

Different goal. A VPN gives you **one** outbound IP (the VPN server).
FlareX gives you **hundreds**, rotating per-request, distributed across
Cloudflare's global PoP network. It's the right tool for scanning / fuzzing
/ IP-hopping; it's the wrong tool for "I want a persistent home network
tunnel".

## Performance characteristics

From `internal/proxy/bench_test.go` on AMD Ryzen 7 5800X:

| Benchmark | ns/op | allocs/op |
|-----------|-------|-----------|
| `NextRR` (healthy pool of 32) | 47 | 0 |
| `IsKnownUnreachable` miss | 29 | 0 |
| `auth.Sign` (HMAC-SHA256) | 871 | 15 |
| E2E tunnel (local mock Worker) | 145 us | 646 |

Throughput at `concurrency=200`: ~11k RPS against a local mockworker.
Bottleneck is the mock's HTTP parser, not FlareX. Production throughput is
bound by CF Worker invocation latency (~50-150ms round-trip).
