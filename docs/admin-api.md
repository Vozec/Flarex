[Back to docs](README.md)

# Admin HTTP API

When `admin.addr` is set, FlareX exposes an HTTP server with operational
endpoints. Binding defaults to loopback-only; set
`admin.addr: 0.0.0.0:9090` only with auth enabled
([configuration.md#admin](configuration.md#admin)).

> Live curl examples with anonymized output for every endpoint:
> **[recipes.md §5](recipes.md#5-admin-api)**.

## Authentication

Five paths into the auth middleware, checked in this order:

1. **Session cookie** (`flarex_session`), set by `POST /session`. The SPA
   uses this; CLI and scrapers don't.
2. `Authorization: Bearer <admin.token>`: bootstrap, all scopes.
3. `X-API-Key: <admin.api_key>` *or* `Authorization: ApiKey <admin.api_key>`:
   bootstrap, all scopes.
4. HTTP Basic with `admin.basic_user` + `admin.basic_pass`: bootstrap.
5. **Scoped named keys** in the `api_keys` registry. SHA-256 hashed at rest;
   send raw value via `X-API-Key` or `Authorization: Bearer`. Each key carries
   a subset of `[read, write, apikeys, pprof]` plus optional expiry.

If all bootstrap fields are empty AND no named key is registered, the
server runs **unauthenticated**. Only safe bound to `127.0.0.1`.

Unauthenticated requests to auth-required endpoints return `401`.
`WWW-Authenticate` is `Basic` only when `admin.basic_user` is configured;
otherwise `Bearer` (browsers don't pop a credential prompt for Bearer 401s).

### Mutations and CSRF

Cookie-authed callers must echo the `flarex_csrf` cookie value as
`X-CSRF-Token` on every non-GET request (double-submit pattern). Header
auth (Bearer / X-API-Key / Basic) skips the CSRF check since they're
authenticated per-request already.

### Brute-force lockout

10 failed `resolveScopes` attempts in 60 s per client IP triggers a 5-minute
`429`. Applied uniformly to `/session` and to any 401 path. The counter
resets on the next successful auth.

### 2FA (TOTP)

When `admin.totp_secret` is set (base32, e.g. from `oathtool` or Google
Authenticator), `POST /session` requires a `totp_code` field in addition
to the api_key. Wrong / missing code returns 401 with body
`{"error":"...","totp_required":true}`.

### Required scope per request

| Path pattern | Method | Scope |
|--------------|--------|-------|
| `/debug/pprof/*` | any | `pprof` |
| `/apikeys/*` | GET | `read` |
| `/apikeys/*` | POST/PATCH/DELETE | `apikeys` |
| `/health` | any | (public) |
| anything else | GET | `read` |
| anything else | POST/PUT/PATCH/DELETE | `write` |

## Endpoints

### `GET /health` · unauth
Liveness probe. Always returns `200 ok`. Use in Docker / k8s healthchecks.

### `GET /metrics` · auth
Prometheus text format. Metric names prefixed `cft_` (legacy, left
unchanged to preserve existing Grafana dashboards after the rename).

Scrape with:
```yaml
scrape_configs:
  - job_name: flarex
    metrics_path: /metrics
    static_configs:
      - targets: ["flarex:9090"]
    bearer_token_file: /etc/flarex/admin-token
```

Full metric list: [observability.md](observability.md).

### `GET /status` · auth
JSON snapshot of the Worker pool:

```json
{
  "pool_size": 10,
  "workers": [
    {
      "name": "flarex-abc123",
      "url": "https://flarex-abc123.subdomain.workers.dev",
      "account": "acc_id",
      "healthy": true,
      "breaker": "closed",
      "inflight": 2,
      "requests": 1523,
      "errors": 4,
      "err_rate_ewma": 0.012,
      "age_sec": 1834
    }
  ]
}
```

Use it to:
- spot a flapping Worker (`err_rate_ewma` trending up).
- confirm rotation is happening (`age_sec` low, count stable).
- gauge per-account distribution.

### `GET /metrics/history?days=N&account=ID` · auth
Time-series from the `quota_history` bbolt bucket. `days` 1-365 (default 7),
`account` optional filter.

```json
{
  "days": 7,
  "account": "",
  "series": [
    {"date": "2026-04-09", "account_id": "acc1", "used": 48231, "limit": 100000},
    ...
  ]
}
```

Snapshots persist every 10 min. Use for a custom dashboard.

### `POST /tokens` · auth · scope `write`
Register a new CF token at runtime. FlareX validates the token (CF
`ListAccounts` + `GetSubdomain`), checks for duplicates against
`cfg.Accounts`, and deploys `worker.count` Workers per resolved account
without restarting.

```bash
curl -X POST -H "X-API-Key: $KEY" \
     -d '{"token":"cf_xxx","count":3}' \
     http://flarex:9090/tokens
```

Response:
```json
{"deployed_workers": ["flarex-abcd", "flarex-efgh"]}
```

Status codes mapped to error category for UI / scripting:

| Status | Cause |
|--------|-------|
| 200 | success |
| 400 | empty body / `{"token":""}` |
| 401 | invalid token (CF code 9109), surfaced as `"token validation failed: list accounts: cf api: 9109 …"` |
| 409 | account already registered (dedup); body says **use `/accounts/{id}/deploy` instead** |
| 502 | other upstream failure (deploy aborted mid-flight, etc.) |

**Use case:** add capacity (new CF accounts) during a scan without
dropping current connections.

### `DELETE /tokens?account=ID` · auth
### `DELETE /tokens?token=VALUE` · auth
Graceful remove. Marks all workers for that account as unhealthy, waits for
inflight drain, deletes from CF, updates state.

```bash
curl -X DELETE -H "X-API-Key: $KEY" \
     "http://flarex:9090/tokens?account=acc_id"
```

### `GET /workers/{name}/logs` · auth
**Server-Sent Events** stream of real Worker logs via the Cloudflare Tail API.
Each event line is a JSON log record from `console.log` / `console.error`
inside the Worker runtime.

```bash
curl -N -H "X-API-Key: $KEY" \
     http://flarex:9090/workers/flarex-abc123/logs
```

Output:
```
data: {"timestamp":1712345678000,"outcome":"ok","logs":[{"message":["dial host=example.com port=443"],"level":"log"}]}

data: {"timestamp":1712345679000,"outcome":"ok","event":{"request":{"url":"..."}}}
```

**Use it to:** debug a Worker's behavior live. Each active `/logs`
subscription opens a CF tail (1-min inactivity, CF auto-expires), so don't
leave dozens open.

**Gotcha:** CF limits 10 concurrent tails per account.

### `GET /accounts` · auth · scope `read`
Per-account aggregate from the live pool.

```json
{"accounts":[
  {"id":"acc_id","name":"Arthur's Account","workers":7,"healthy":7,"quota_paused":0}
]}
```

`name` is sourced from CF discovery. Useful for displaying multi-account
deployments without staring at hex IDs.

### `POST /accounts/{id}/pause` · `/resume` · scope `write`
Mass-toggle `QuotaPaused` on every worker of the account. Used by the
admin UI's per-account drawer and the global kill switch.

```bash
curl -X POST -H "X-API-Key: $KEY" \
     http://flarex:9090/accounts/acc_id/pause
```

Response: `{"account":"acc_id","paused":true,"affected":7}`.

In-flight connections finish on the current worker; new dials skip the
paused workers and route to other accounts (or fail with
`no workers available` if every account is paused).

### `POST /accounts/{id}/deploy` · scope `write`
Scale an existing account WITHOUT re-pasting its token (uses the token
already in `cfg.Accounts`).

```bash
curl -X POST -H "X-API-Key: $KEY" \
     -d '{"count":2}' \
     http://flarex:9090/accounts/acc_id/deploy
```

Response: `{"account":"acc_id","deployed_workers":["flarex-..."]}`.

`count` defaults to `cfg.worker.count` when omitted or `<= 0`. Rejects
with 503 if `DeployMoreFunc` isn't wired (server compiled without it),
or 500 with the underlying CF error on deploy failure.

### `POST /workers/{name}/recycle` · scope `write`
Graceful drain + redeploy of a single worker. Returns `{old, new}`.

```bash
curl -X POST -H "X-API-Key: $KEY" \
     http://flarex:9090/workers/flarex-abc/recycle
```

### `GET /metrics/series` · scope `read`
In-memory ring buffer of per-15-second snapshots (60 slots = 15-minute
window). Same data feeds the Overview charts.

```json
{"samples":[
  {"at":"2026-04-16T15:27:30Z","connections":7,"dial_success":7,
   "dial_fail":0,"handshake_fail":0,"bytes_upstream":13398,
   "bytes_downstream":29047,"fetch_fallback":0,"hedge_fired":2,
   "hedge_wins":2,"latency_p50_ms":42.1,"latency_p95_ms":118.0,
   "latency_p99_ms":280.0,"worker_requests":{"flarex-abc":12,...}}
]}
```

Persisted to bbolt so a restart doesn't blank the chart. Old samples
pruned at 7 days.

### `GET /audit?limit=N` · scope `read`
Most recent admin mutations, newest first. Sensitive query parameters
(`api_key`, `token`, `secret`, `password`, …) in URLs are redacted to
`****` before persistence.

```json
{"events":[
  {"at":"2026-04-16T15:41:11Z","who":"session:bootstrap:api_key",
   "action":"test.request","target":"https://httpbin.org/get",
   "detail":"flarex-abc"},
  ...
]}
```

### `GET /config` · `PATCH /config` · scope `read` / `write`
GET returns the sanitized running config (secrets masked). PATCH applies
a single field update.

```bash
# GET
curl -H "X-API-Key: $KEY" http://flarex:9090/config

# PATCH
curl -X PATCH -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
     -d '{"path":"pool.proxy_mode","value":"fetch"}' \
     http://flarex:9090/config
```

Response: `{"path":"pool.proxy_mode","applied":true,"requires_restart":false}`.

Errors:
- 400: unknown path / type mismatch / out-of-range value (clear text in `error`)
- 503: runtime updates not wired

Live-applied paths swap state on running components (`IPFilter`,
`proxy.Server`); restart-required paths update `cfg` so a `flarex
backup` + restart picks them up. Full registry: `cmd/flarex/config_update.go`.

### `POST /config/proxy-mode` · scope `write`
Shortcut for `PATCH /config {"path":"pool.proxy_mode","value":...}`.

```bash
curl -X POST -H "X-API-Key: $KEY" \
     -d '{"mode":"hybrid"}' http://flarex:9090/config/proxy-mode
```

### `POST /test-request` · scope `write` · UI-only (404 when `admin.ui: false`)
Send a single GET through the live pool from the server itself. Reuses
`DialWithPolicy` so it picks up the current proxy mode, breakers, hedge,
etc.

```bash
curl -X POST -H "X-API-Key: $KEY" \
     -d '{"url":"https://httpbin.org/get"}' \
     http://flarex:9090/test-request
```

Response:
```json
{
  "worker": "flarex-abc","colo":"CDG","egress_ip":"104.28.164.242",
  "status": 200,"latency_ms": 188,"mode": "socket","headers": {...},
  "body": "...","resolved_host":"httpbin.org"
}
```

`egress_ip` is parsed from response headers (`Cf-Connecting-Ip`,
`X-Forwarded-For`) or sniffed from the body (`{"ip":"…"}`, cf/trace
key=value, ifconfig.me plain). `body` is capped at 4 KB
(`body_trunc_at` is set when the cap kicked in). On
`ErrUpstreamBlocked` (CF-hosted target) the handler auto-promotes to
`fetch` mode and retries.

### `GET /test-history` · scope `read` · UI-only
Last 100 `/test-request` runs with full result, newest first.

### `GET / POST / PATCH / DELETE /apikeys[/{id}]` · UI-only
Scoped key registry. GET `/apikeys` lists keys with hash redacted (only
the 12-char prefix is shown). POST creates and returns the raw key
**once**. PATCH toggles `disabled`. DELETE revokes immediately.

```bash
# Create
curl -X POST -H "X-API-Key: $KEY" -d '{"name":"scraper","scopes":["read"],"expires_in":"720h"}' \
     http://flarex:9090/apikeys
# → {"id":"01H...", "key":"flx_<32 random chars>", "prefix":"flx_xxxx",
#    "scopes":["read"], "name":"scraper", "expires_at":"...", ...}
```

Mutations are rate-limited to 10/min per session/IP to slow privilege
escalation if a session is hijacked. POST with a duplicate `name` returns
`409`.

### `POST / DELETE /session` · public (POST is rate-limited)
Login / logout for the SPA. POST body is `{"api_key":"...","totp_code":"…"}`.
Sets `flarex_session` (HttpOnly) + `flarex_csrf` cookies. DELETE clears them.

If `admin.totp_secret` is set and the code is missing/wrong, the response
is `401` with body `{"error":"…","totp_required":true}` so the SPA can
render the 6-digit input.

### `POST /alerts/webhook` · scope `write`
Receives an Alertmanager v4 webhook payload and forwards each alert
through the existing alerts dispatcher (Discord, HTTP webhooks defined
in `cfg.alerts`). Lets you reuse FlareX's alert sinks for cluster-wide
alerts without duplicating webhook URLs.

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

Response: `{"received":N}` where N is the number of alerts forwarded.

### `GET /debug/pprof/*` · auth · requires `enable_pprof: true`
Standard Go `net/http/pprof` endpoints:

```bash
go tool pprof "http://user:pass@flarex:9090/debug/pprof/heap"
go tool pprof -http :8080 "http://flarex:9090/debug/pprof/profile?seconds=30"
```

pprof leaks binary internals (symbols, goroutine stacks). Never
enable unauthenticated.

## Example: quick status via CLI

The `flarex client` wrapper does all of the above with persisted creds:

```bash
flarex client login --url http://flarex:9090 --api-key XXX
flarex client status
flarex client add-token --token cf_newacc_xxx
flarex client logout
```

See [cli.md](cli.md#remote-admin-flarex-client-).
