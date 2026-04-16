[← Back to docs](README.md)

# Benchmarks

Reproducible performance measurements for FlareX hot paths. All numbers
from AMD Ryzen 7 5800X (Linux 6.19), `go1.25`.

## Running the suite

```bash
# Micro-benchmarks (no network)
make bench
# Or directly
go test -bench=. -benchmem -benchtime=3s -run=^$ ./internal/...
```

Individual focused runs:

```bash
go test -bench=BenchmarkNextRR -benchmem ./internal/pool
go test -bench=BenchmarkSign   -benchmem ./internal/auth
go test -bench=BenchmarkIsKnown -benchmem ./internal/proxy
go test -bench=.Throughput -benchtime=10s ./internal/proxy
```

## Baseline

### Worker selection (hot path, called per client conn)

```
BenchmarkPoolNextRR_Parallel-16       53474466    21.71 ns/op    0 B/op   0 allocs/op
BenchmarkPoolAll-16                 1000000000     0.05 ns/op    0 B/op   0 allocs/op
BenchmarkNextRR_Healthy-16            23466052    46.84 ns/op    0 B/op   0 allocs/op
BenchmarkNextRR_PartialUnhealthy-16   24980292    47.92 ns/op    0 B/op   0 allocs/op
BenchmarkNextRR_Parallel-16           55234077    22.54 ns/op    0 B/op   0 allocs/op
```

Takeaway: pool read is zero-alloc, ~50ns for sequential, ~22ns parallel
(atomic.Pointer deref). Scales linearly with cores.

### HMAC signing (called per dial)

```
BenchmarkSign-16    1395862    870.7 ns/op    832 B/op    15 allocs/op
```

~1 us and 15 allocs. Room for improvement with a stack-allocated buffer +
manual itoa, but it's not the bottleneck (WebSocket dial is ~1000x slower).

### CF-unreachable cache (called per dial)

```
BenchmarkIsKnownUnreachable_Miss-16  40201478   28.57 ns/op   0 B/op   0 allocs/op
BenchmarkIsKnownUnreachable_Hit-16   21014204   56.61 ns/op   0 B/op   0 allocs/op
BenchmarkMarkUnreachable-16           4984230  235.4 ns/op  104 B/op   5 allocs/op
```

Miss: one `sync.Map.Load` (~29ns). Hit: load + time comparison (~57ns).
Mark: `sync.Map.Store` with time allocation.

### End-to-end tunnel (local mockworker)

```
BenchmarkE2E-16                          8175   144828 ns/op   748884 B/op    646 allocs/op
BenchmarkThroughput/conc=1-16            100    10002299 ns/op   fail_total=0    ok_total=1474   rps=1474
BenchmarkThroughput/conc=10-16           100    10012138 ns/op   fail_total=0    ok_total=5240   rps=5234
BenchmarkThroughput/conc=50-16           118    10019879 ns/op   fail_total=0    ok_total=12005  rps=10154
BenchmarkThroughput/conc=200-16          114    10091044 ns/op   fail_total=0    ok_total=12845  rps=11166
```

Interpretation:

- 145 us per tunnel end-to-end including SOCKS5 handshake, Worker WS dial,
  round-trip, close.
- Throughput saturates around **~11k RPS** at 200 concurrency. Bottleneck
  is the mockworker's HTTP parser + Go scheduler, not FlareX itself.
- Zero failures across 12k tunnels in 10s = proxy is stable under load.

## What each benchmark measures

See `internal/proxy/bench_test.go` + `internal/proxy/helpers_test.go`.

- `BenchmarkE2E` spins up a real mockworker (implements the same WS + HMAC
  protocol as the CF Worker template) and a target HTTP server on loopback.
  Each op = 1 full tunneled GET.
- `BenchmarkThroughput` runs N parallel tunnels for 10s, counts successes
  + failures + RPS.
- `BenchmarkNextRR_*` sanity-check pool scaling under various health
  conditions.

## Profiling

```bash
# CPU profile of the throughput bench
go test -bench=.Throughput -cpuprofile=cpu.prof -run=^$ ./internal/proxy
go tool pprof -http :8080 cpu.prof

# Memory (allocs)
go test -bench=.E2E -memprofile=mem.prof -run=^$ ./internal/proxy
go tool pprof -http :8080 mem.prof
```

On a running production proxy: admin HTTP `/debug/pprof/*` with
`enable_pprof: true`. See [observability.md](observability.md).

## Local end-to-end bench with real HTTP target

See `scripts/bench_local.sh`. Spins up:

- `python3 -m http.server` as the target.
- `mockworker` as a fake CF Worker.
- `flarex server` on port 11080.
- `curl` through the SOCKS5 proxy in a loop, reporting RPS.

All on loopback, no CF API calls, no network. A/B-test code changes
without burning quota.

```bash
./scripts/bench_local.sh
# Output: RPS, p50/p95/p99 latency, total errors
```

## Against real Cloudflare

Not a benchmark per se, more of a sanity check. With your config pointing
at real Workers:

```bash
# Warm the HTTP/2 pool first (keep-alive loop does this automatically
# within ~1 min of startup, or hit a few times manually)
for i in {1..10}; do curl -x socks5h://127.0.0.1:1080 https://ifconfig.me; done

# Then time:
time for i in {1..100}; do
  curl -s -x socks5h://127.0.0.1:1080 https://ifconfig.me >/dev/null
done
```

Expect ~100-200ms per request (CF round-trip dominates). Worker IPs
should rotate across the 100 samples.

## Contributing benchmarks

If you add a feature on the hot path, include a benchmark. CI runs
`make bench` on PRs; regressions >10% should be justified or fixed.

Target: **zero allocs on the hot path** (pool read, sniff, session hash).
Stack-allocated is fine; heap alloc per request is a regression.
