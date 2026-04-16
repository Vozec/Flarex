[Back to docs](README.md)

# Admin web UI

A React SPA (Vite + Tailwind + Recharts + react-simple-maps) embedded in
the Go binary via `//go:embed`. Single binary; no runtime Node
dependency. Opt-in via `admin.ui: true` in your config; default off so
headless deployments stay slim.

## Enable

```yaml
admin:
  addr: "127.0.0.1:9090"   # bind loopback unless auth + firewall are in front
  api_key: "<32+ chars>"   # bootstrap / root credential
  ui: true                 # opt-in
  totp_secret: ""          # optional base32 TOTP; set this for 2FA on login
```

Env override: `FLX_ADMIN_UI=true` · `FLX_ADMIN_TOTP_SECRET=<base32>`.

```bash
make web-build   # one-off: builds the React bundle into internal/admin/webui/dist
make rebuild     # re-embeds the bundle into bin/flarex
./bin/flarex server -c config.yaml
```

Open `http://127.0.0.1:9090/`; redirects to `/ui/`.

If you skip `make web-build`, the binary still runs; `/ui/` shows a
placeholder with the command to fix it.

## First login

Use `admin.api_key` from `config.yaml` (root credential, all scopes). If
`admin.totp_secret` is set, you'll also be asked for a 6-digit code on
the same form (Google Authenticator, `oathtool --totp -b $SECRET`).

After login, head to **API keys** and mint named scoped keys for
day-to-day use. Keep the bootstrap key for recovery only.

## Pages

### Overview

<img src="assets/overview1.png" alt="Overview top half" width="800">
<img src="assets/overview2.png" alt="Overview geo + colo" width="800">

Top tiles: pool size, healthy ratio, quota-paused count, breakers open,
account count.

Below: live throughput row (connections/s, ↑ bytes/s, ↓ bytes/s,
handshake fails), then **fetch fallback** + **hedged dials win-rate** +
**dial failure** counters (sourced from `cft_*` metrics).

Mid: **per-account quota bars**, **egress geography map** (real
natural-earth land polygons, colo pins sized by worker count), and
**workers per colo** horizontal bar chart.

Bottom: **request rate** + **dial latency p50/p95** line charts (15-s
samples × 60 = 15-minute history; persisted to bbolt so a restart
doesn't blank them).

### Accounts

<img src="assets/accounts.png" alt="Accounts cards" width="800">

One card per CF account with:
- Display name (from `discovery.AccountDetails.Name`, with the noisy
  `'s Account` suffix stripped).
- Truncated id + copy button + Cloudflare-dashboard deep-link.
- Healthy / quota-paused ratio pill.
- **Pause / Resume** mass-toggle (sets `QuotaPaused=true|false` on every
  worker of the account).
- **Remove** (drains + deletes every worker, keeps the token usable).

**Click anywhere on the card** (except buttons) to open the side drawer
with: per-account quota bar (today's CF subrequest usage), worker list,
**Add more workers** form (no token re-auth; uses the stored token from
`cfg.Accounts`).

Add-account form at the top: paste a CF token, optionally specify a
count override. Token is **validated** before deploy (CF code 9109
surfaces as an "invalid token" toast) and **deduplicated** (409 with
the resolved account name if it's already registered).

### Workers

<img src="assets/workers.png" alt="Workers table sortable" width="800">

Filter input on top (matches name / colo / account). **Click any column
header to sort** (asc/desc indicator). Bulk-select rows via checkbox, then
**Recycle selected** drains and redeploys each, with progress toast.
**Recycle unhealthy** auto-targets workers reporting `healthy=false`.

Per-row sparkline: last 60 samples of
`cft_worker_requests_total{name=…}` deltas. Useful to spot a worker
soaking up disproportionate traffic (sticky-session stuck) or a worker
that's gone silent.

**Click any row** to open a side drawer with full URL (copy + CF-dashboard link),
account, colo, age, breaker, inflight / requests / errors / EWMA, plus
copy-ready snippets:
- `curl -x socks5h://<auth_user>:$PASS@127.0.0.1:1080 https://ifconfig.me`
  (the SOCKS user is pulled from `/config`; `$PASS` is a placeholder you
  fill in; the server never echoes the password)
- `curl -fsS "<worker-url>/__health"` to probe the Worker directly.

### Test request

<img src="assets/test.png" alt="Test request page" width="800">

Send any HTTP(S) URL through the live pool from the admin server itself
(reuses `DialWithPolicy` so it picks up the current proxy mode,
breakers, hedge, etc.). Returns:

- **Worker** that handled the dial (+ its CF colo).
- **Mode** picked (socket / fetch; auto-promoted to fetch on
  `ErrUpstreamBlocked` for CF-hosted targets).
- **Egress IP** parsed from response headers (`Cf-Connecting-Ip`,
  `X-Forwarded-For`) or sniffed from the response body (ipify JSON,
  cf/trace key=value, ifconfig.me plain).
- **Latency**, **HTTP status pill**, **response headers table**, and
  **body** preview (capped at 4 KB).
- Presets for httpbin / ipify / ifconfig / cf-trace.
- **History**: last 100 runs persisted in bbolt; one-click Replay
  button on each row.

### Logs

Pick a worker for a live SSE stream from Cloudflare's Tail API. Or pick
the top option **`— ALL workers (N) —`** to fan-in N parallel tails into
the same console with per-line `[worker_suffix]` colored prefix (stable
hash gives a stable color per worker).

CF caps ~10 concurrent tails per account; the UI warns when you pick
ALL on a large pool. Trash button clears the buffer.

### Quota

<img src="assets/quota.png" alt="Quota daily history" width="800">

14-day stacked bar of subrequest usage per account, plus today's totals
table with color-coded warn thresholds (75% amber, 90% red). Source =
the `quota_history` bbolt bucket written by the daily snapshotter.

### API keys

<img src="assets/apikeys.png" alt="API keys page" width="800">

Mint scoped keys with optional TTL:

| Scope | Allows |
|-------|--------|
| `read` | Every GET (status, accounts, config, metrics, audit, apikeys list) |
| `write` | POST / DELETE / PATCH on tokens, workers, accounts, config |
| `apikeys` | Create / revoke / disable other API keys (privileged, can escalate) |
| `pprof` | `/debug/pprof/*` (only when `admin.enable_pprof: true`) |

Bootstrap `admin.api_key` / `token` / `basic_*` always carry all
scopes. New keys appear once at creation as `flx_<32-char>`; only the
sha256 hash is stored. Mutations on `/apikeys` are rate-limited
(10/min per session) to slow privilege-escalation attempts.

### Audit log

Every admin mutation is recorded with `{at, who, action, target,
detail}`. Sensitive query parameters in URLs (`api_key`, `token`,
`secret`, `password`, `access_token`, `refresh_token`) are redacted to
`****` before persistence. Export visible rows as CSV with the **CSV**
button (filename `flarex-audit-YYYY-MM-DD.csv`).

### Config (editable)

<img src="assets/config.png" alt="Editable config page" width="800">

Form per section: pool, filter, rate_limit, worker, listener, admin,
quota & alerts, log. Each field uses the appropriate input type (text /
password / number / select / bool / comma-separated array). Save +
Revert buttons appear when a field is dirty.

Each field is tagged `requires_restart` (⚠ badge) or
**live-applied**. Live-applied fields:

- `pool.proxy_mode`, `pool.max_retries`, `pool.backoff_ms`,
  `pool.hedge_after_ms`, `pool.tls_rewrap`
- `filter.allow_ports` (atomic swap on the running `IPFilter`)
- `worker.count`, `worker.name_prefix`, `worker.deploy_backend`
  (affects next deploy only)
- `quota.warn_percent`

Restart-required fields update the in-memory cfg so a `flarex backup` +
restart picks them up, but the running listener / pool needs a restart
to honor them. Type / range mismatches return 400 with the message
shown in the toast.

### Test header

<img src="assets/overview1.png" alt="TopBar with kill switch + proxy mode pill + theme toggle" width="800">

Top bar widgets:
- **`mode: hybrid`** pill: click to cycle proxy mode (with confirm
  dialog). Same as PATCH `/config/proxy-mode`.
- **`kill` / `resume`**: emergency global kill switch (pauses every
  worker on every account in one click).
- **Theme toggle** (sun/moon): light/dark, persisted in `localStorage`,
  defaults to `prefers-color-scheme`.
- **Logout** clears the session cookie.

### Cmd+K command palette

Press **⌘K** / **Ctrl+K** anywhere for fuzzy search over nav routes,
actions (kill all / resume all / recycle unhealthy), workers, accounts.
Arrow keys + Enter; Escape closes.

## Mounting

The SPA lives at `/ui/*`. All backend endpoints are mounted at their
historical paths (**not prefixed with `/api/`**) so `flarex client`
and Prometheus scrapers keep working unchanged.

Inside the SPA, hash-based routing (`/ui/#/accounts`) sidesteps
server-side rewrites; the Go file server only needs to serve
`index.html` for any unknown `/ui/*` path.

## Development

```bash
# Terminal 1: run FlareX admin on :9090
./bin/flarex server -c config.yaml

# Terminal 2: Vite dev server with HMR, proxying API calls → :9090
make web-dev
# opens http://localhost:5173 with hot reload
```

When ready, `make web-build && make rebuild` embeds the production
bundle. CI runs `make web-build` automatically before `go build`.

## Security notes

- **Loopback by default.** `admin.addr: 127.0.0.1:9090`. Don't bind to
  `0.0.0.0` without a reverse proxy handling TLS + IP allowlisting.
- **Cookie session, not localStorage.** Login sets an HttpOnly +
  SameSite=Strict cookie signed with a key derived from
  `security.hmac_secret`. CSRF is enforced on every mutation via the
  double-submit pattern (`X-CSRF-Token` header + `flarex_csrf` cookie).
  Sessions auto-refresh on activity (rolling TTL); idle sessions still
  expire within 24 h.
- **Brute-force protection.** 10 failed logins / 60 s per IP triggers a
  5 min lockout. Applies to `/session` and any 401 response on authed
  endpoints.
- **2FA when it matters.** Set `admin.totp_secret` to require a TOTP
  code on every login. The secret never appears on the wire.
- **`apikeys` scope is root-equivalent.** A key with that scope can
  mint a new key with any scope set. Hand it out sparingly.
- **`pprof` leaks runtime internals.** Only enable
  (`admin.enable_pprof: true`) when actively profiling.
- **Audit log redacts URL credentials.** `?api_key=…` etc. become
  `****` before write; the request body is not scrubbed, so mind
  what you POST to `/test-request`.

## Binary size

The embedded bundle adds ~200 KB gzipped to `bin/flarex` (initial
chunk; the geo map chunk lazy-loads only on the Overview tab). When
`admin.ui: false`, the bundle still ships in the binary but nothing
serves it, and the UI-only endpoints (`/apikeys`, `/test-request`,
`/test-history`, `/accounts/*/pause|resume|deploy`,
`/config/proxy-mode`, `/metrics/series`) all return 404.
