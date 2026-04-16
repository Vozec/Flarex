[Back to repo README](../README.md)

# FlareX documentation

FlareX is a SOCKS5 + HTTP CONNECT proxy that routes outbound traffic
through a swarm of dynamically deployed Cloudflare Workers.

## If you're new

Read in order:

1. **[Cloudflare token](cloudflare-token.md)**: get a token with the right scopes.
2. **[CLI reference](cli.md)**: `flarex deploy`, `server`, `destroy`, `client`.
3. **[Configuration](configuration.md)**: every YAML key + env var.
4. **[Proxy modes](proxy-modes.md)**: `socket` vs `fetch` vs `hybrid`.
5. **[Troubleshooting](troubleshooting.md)**: the error you're about to hit is probably here.

## By topic

| Topic | Doc |
|-------|-----|
| Connection flow + retry + hedge + breaker | [architecture.md](architecture.md) |
| Running in Docker / Portainer / systemd | [deployment.md](deployment.md) |
| Admin HTTP endpoints (metrics, tokens, logs) | [admin-api.md](admin-api.md) |
| Admin web UI (React SPA: charts, accounts, API keys) | [admin-web-ui.md](admin-web-ui.md) |
| Hybrid-mode byte-sniffing + CF-hosted targets | [proxy-modes.md](proxy-modes.md) |
| Sticky sessions for cookie/auth flows | [session-stickiness.md](session-stickiness.md) |
| uTLS JA3 rotation | [tls-rewrap.md](tls-rewrap.md) |
| Prometheus, OpenTelemetry, log format | [observability.md](observability.md) |
| Why some things don't work (UDP, AUP, CF IPs) | [cloudflare-limitations.md](cloudflare-limitations.md) |
| Benchmark methodology + baseline | [benchmarks.md](benchmarks.md) |

## Repo layout

```
cmd/flarex/              # CLI entry point
cmd/mockworker/          # Local stand-in Worker for benchmarks
internal/
  admin/                 # Admin HTTP server
  alerts/                # Webhook + Discord quota alerts
  auth/                  # HMAC signing between proxy and Worker
  backend/               # workers_dev + custom_domain deploy backends
  cfapi/                 # Cloudflare REST wrapper (scripts, subdomains, DNS, tails)
  config/                # YAML + env parsing (koanf)
  discovery/             # Auto-discover account id / subdomain from a token
  dnscache/              # In-process DNS cache with background refresh
  filter/                # IP + port allowlist / denylist
  logger/                # zerolog wrapper + banner art
  metrics/               # Prometheus counters + histograms
  pool/                  # Worker pool (atomic snapshot) + circuit breaker + quota
  proxy/                 # SOCKS5 + HTTP CONNECT + dial + sniff + tunnel
  ratelimit/             # Per-host token bucket
  scheduler/             # round_robin / least_inflight worker picker
  state/                 # bbolt persistence (workers + quota history)
  tlsdial/               # uTLS wrapper for JA3 rotation
  tracing/               # OTLP tracer bootstrap
  worker/                # Worker deploy/destroy/rotate + JS template
```
