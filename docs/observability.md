[← Back to docs](README.md)

# Observability

What FlareX emits and where to point it.

> Live recipes: [5.9 Prometheus scrape](recipes.md#59-prometheus-metrics-scrape) · [5.10 quota history](recipes.md#510-quota-usage-history-per-account) · [5.17 worker pool table/json](recipes.md#517-worker-pool-human-table-vs-machine-json).

## Logs

**Library:** [`rs/zerolog`](https://github.com/rs/zerolog).
**Format:** human-readable console by default (colored), JSON via
`log.json: true`.
**Destination:** stderr (console) or stdout (JSON).

```
00:15:55 INF pool loaded auto_deploy=false destroy_on_exit=false pool_size=2
```

Levels:

| Level | What you see |
|-------|--------------|
| `trace` | Very verbose. Dumps packet-level decisions. Leaks host names. |
| `debug` | Per-request decisions: worker selection, fallback reasons, hedge fire. |
| `info` | Lifecycle: startup, deploy, rotation, quota warning. |
| `warn` | Recoverable: dial retry, breaker trip, drain timeout. |
| `error` | Fatal condition before crash (rare; most errors are warnings). |

**JSON format for ingest** (`log.json: true` in config):
```json
{"level":"info","time":"2026-04-16T00:15:55Z","pool_size":2,"message":"pool loaded"}
```

For Loki, Datadog, Elasticsearch, etc.

## Prometheus metrics

Exposed at `GET /metrics` on the admin HTTP (requires auth). Metric names
are `cft_*` (legacy prefix, left unchanged after the FlareX rename so
existing dashboards keep working).

### Counters

| Metric | Meaning |
|--------|---------|
| `cft_connections_total` | Client conns accepted (SOCKS5 + HTTP CONNECT). |
| `cft_connections_active` | Currently-active tunnels (gauge pattern). |
| `cft_filter_denied_total` | Rejected by `filter.deny_cidrs` / `allow_ports`. |
| `cft_handshake_fail_total` | SOCKS5 or HTTP CONNECT handshake failed. |
| `cft_dial_fail_total` | All retries exhausted on outbound dial. |
| `cft_dial_success_total` | Successful upstream tunnels. |
| `cft_bytes_upstream_total` | Bytes sent client → target. |
| `cft_bytes_downstream_total` | Bytes sent target → client. |
| `cft_worker_requests_total{name="flarex-abc"}` | Per-Worker request count. |
| `cft_fetch_fallback_total` | Hybrid-mode socket→fetch promotions (CF-hosted target sniffed as HTTP). |
| `cft_dial_hedge_fired_total` | Hedge worker started (primary still pending). |
| `cft_dial_hedge_wins_total` | Hedge worker returned the connection first. |

### Histograms

| Metric | Meaning |
|--------|---------|
| `cft_dial_latency_seconds` | Time to first successful byte through a Worker. |
| `cft_request_duration_seconds` | End-to-end tunnel duration (close). |

### Bundled Grafana dashboard

[`deploy/grafana/flarex.json`](../deploy/grafana/flarex.json): 12-panel
dashboard covering connection rate, dial latency p50/p95/p99, throughput
up/down, fetch-fallback rate, hedge fired/wins, filter denials, top-10
workers by request rate, plus the standard `go_*` / `process_*` runtime
panels.

Import via Grafana **Dashboards → New → Import → Upload JSON file**. It
expects a Prometheus datasource named `prometheus`; rename or remap if
yours differs.

### Live in-memory ring (no Prometheus required)

`GET /metrics/series` returns the last 60 samples (15-second cadence,
15-minute window) of every counter + histogram quantile. Persisted to
bbolt so a server restart doesn't blank the chart. The admin SPA's
Overview tab consumes this directly.

```bash
curl -H "X-API-Key: $KEY" http://flarex:9090/metrics/series | jq '.samples | last'
```

### Example dashboards

PromQL snippets:

```promql
# Tunnel success rate (5 min)
rate(cft_dial_success_total[5m]) /
  (rate(cft_dial_success_total[5m]) + rate(cft_dial_fail_total[5m]))

# p95 dial latency
histogram_quantile(0.95, sum by (le) (rate(cft_dial_latency_seconds_bucket[5m])))

# Bandwidth (both directions)
rate(cft_bytes_upstream_total[1m]) + rate(cft_bytes_downstream_total[1m])

# Hottest workers
topk(5, rate(cft_worker_requests_total[5m]))

# Hybrid fallback rate: % of dials that needed fetch promotion
rate(cft_fetch_fallback_total[5m]) / rate(cft_dial_success_total[5m])

# Hedge effectiveness: % of dials where the hedge actually won the race
rate(cft_dial_hedge_wins_total[5m]) / rate(cft_dial_hedge_fired_total[5m])
```

### Scrape config

```yaml
scrape_configs:
  - job_name: flarex
    metrics_path: /metrics
    bearer_token: "<admin.token>"
    static_configs:
      - targets: ["flarex:9090"]
```

## OpenTelemetry traces

Enable by setting `tracing.endpoint` to an OTLP/gRPC collector:

```yaml
tracing:
  endpoint: "localhost:4317"
  insecure: true           # plaintext; false = TLS
```

### What gets traced

One span per outbound dial attempt, named `proxy.dial`.

Attributes at span start:
- `target.host`: destination hostname or IP.
- `target.port`.

Attributes at span end (on success):
- `worker`: which Worker handled it.
- `mode`: `socket` or `fetch`.
- `retry_count`: how many Workers were tried (0 = first succeeded).

### Sampling

`AlwaysSample()` for now. Every dial = one span. High-volume deployments
will need downsampling; that's a future config knob.

### Collector setup

A minimal OTel-Collector config:

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
processors:
  batch:
exporters:
  otlp/tempo:
    endpoint: tempo:4317
    tls: {insecure: true}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/tempo]
```

Traces then land in Tempo / Jaeger / Grafana Cloud.

## Alerts (quota + webhook)

Driven by the `alerts` config block. Event kinds:

| Kind | Fires when |
|------|------------|
| `quota_warn` | Account usage crosses `quota.warn_percent` (default 80%). |
| `quota_limit` | Account hits 100% daily quota. Workers auto-pause. |
| `worker_down` | A Worker's circuit breaker opens (5+ consecutive failures or >50% err rate). |

### Sinks

- **HTTP webhook**: POST JSON:
  ```json
  {
    "kind": "quota_warn",
    "account_id": "acc1",
    "account_name": "...",
    "message": "Crossed 80% of daily quota",
    "used": 80123,
    "limit": 100000,
    "at": "2026-04-16T12:34:56Z"
  }
  ```
  Configure `alerts.http_webhooks[].url` + custom `headers` (auth, routing).

- **Discord webhook**: rich embed with progress bar, color-coded by severity
  (yellow = warn, red = limit, grey = info).

### Cooldown

Duplicate alerts for the same `(kind, account_id)` within
`alerts.cooldown_sec` (default 900s) are suppressed. Prevents notification
loops when a worker flaps. Implementation: `sync.Map` + CAS in
`alerts/alerts.go`.

### Alertmanager bridge

`POST /alerts/webhook` accepts the standard Alertmanager v4 payload and
forwards each alert through the same dispatcher (Discord + HTTP
webhooks). Lets you use FlareX's webhook URLs for cluster-wide alerting
without duplicating them in Alertmanager config.

```yaml
# alertmanager.yml
receivers:
  - name: flarex
    webhook_configs:
      - url: https://flarex.internal/alerts/webhook
        http_config:
          authorization:
            type: Bearer
            credentials: <admin.token>
```

The forwarded `Event.Kind` is `alertmanager.<status>` (e.g.
`alertmanager.firing`), AccountID = receiver name, Message = `[severity]
summary`, so the cooldown still dedups noisy alerts.

## Quota history

Every 10 minutes the server snapshots per-account usage into the bbolt
`quota_history` bucket. Key: `"YYYY-MM-DD|<account>"`. Value: `QuotaDay`
JSON.

Query via the admin API:

```bash
curl -H "X-API-Key: $KEY" \
     "http://flarex:9090/metrics/history?days=30&account=acc1"
```

Use case: monthly reporting, billing reconciliation, detecting "usage
spiked on a specific day".

## pprof

Gate with both `admin.enable_pprof: true` AND auth. Standard Go profiles:

- `/debug/pprof/heap`: memory allocations.
- `/debug/pprof/profile?seconds=30`: 30s CPU profile.
- `/debug/pprof/goroutine`: goroutine dump.
- `/debug/pprof/trace?seconds=10`: execution trace.

**Must-know:** `go tool pprof -http :8080 http://user:pass@flarex:9090/debug/pprof/heap`
needs the auth inline. Bearer tokens aren't passed by `go tool pprof`.
