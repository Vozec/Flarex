[← Back to docs](README.md)

# Configuration reference

Full tour of `config.yaml`. Every key has: **type · default · what it does ·
when to change it · gotcha**. Everything is overridable via `FLX_*` env vars
(see [environment variables](#environment-variables)).

See [`config.example.yaml`](../config.example.yaml) for a ready-to-fill
template.

---

## `log`

| Key | Default | Purpose / when to change |
|-----|---------|--------------------------|
| `level` | `info` | `trace`/`debug`/`info`/`warn`/`error`. Use `debug` to diagnose dial failures; `trace` is noisy + may leak host names. |
| `json` | `false` | Switch to JSON on stdout for Loki/Datadog. Console-style stays on stderr otherwise. |

## `listen`

| Key | Default | Purpose / when to change |
|-----|---------|--------------------------|
| `socks5` | `tcp://127.0.0.1:1080` | `tcp://host:port` or `unix:///path`. Bind to `0.0.0.0` only if you front it with auth + firewall. |
| `http` | `""` (disabled) | Same format. Enables an HTTP CONNECT frontend in addition to SOCKS5. |
| `auth_user` / `auth_pass` | `""` | SOCKS5 user/pass + HTTP Basic. Set BOTH or NEITHER. Extract session suffix via `user-session-<id>` (see [session-stickiness.md](session-stickiness.md)). |
| `unix_perms` | `0o600` | Only applies when `socks5` or `http` is a `unix://` socket. `0o660` lets a group read/write. |

**Gotcha:** binding `0.0.0.0:1080` without auth = open proxy. The defaults
bind to loopback on purpose.

## `tokens` (bare tokens)

```yaml
tokens:
  - "cf_api_token_with_workers_edit_scope"
```

List of Cloudflare API tokens. At startup FlareX discovers each token's
account IDs + `workers.dev` subdomains. **Preferred**: one token per
account, nothing else to configure.

## `accounts` (explicit accounts)

```yaml
accounts:
  - id: "acc_id"
    token: "cf_api_token"
    subdomain: "your-subdomain"
```

Use when you want to pin identifiers instead of discovering them. Required if
the token has access to multiple accounts and you only want to deploy to some.

You can mix `tokens[]` and `accounts[]`; duplicates are deduplicated by
`(id, token)`.

## `worker`

| Key | Default | Purpose / when to change |
|-----|---------|--------------------------|
| `name_prefix` | `flarex-` | Prefix for deployed Worker names. Filter scope for `destroy`, `clean`, `list`, rotation, log tail. Change if multiple FlareX instances share an account. |
| `count` | `10` | Workers per account. More = more IP diversity + higher parallelism; also more CF quota used. Practical range: 2–100. |
| `deploy_timeout` | `30` seconds | Deploy step timeout (upload script + bind subdomain). |
| `template_path` | `""` | Path to a custom Worker JS file. Empty = embedded `template.js`. |
| `rotate_interval_sec` | `0` (off) | How often the rotator runs. `rotate_max_*` only evaluate on each tick. `300` = check every 5 min. |
| `rotate_max_age_sec` | `0` (off) | Recycle Workers older than N seconds. Fresh CF IPs every N sec. |
| `rotate_max_req` | `0` (off) | Recycle after N requests on one Worker. Use when a Worker gets sticky-blocked by a target. |
| `deploy_backend` | `auto` | `workers_dev` / `custom_domain` / `auto`. See [deployment.md](deployment.md). |

## `filter`

| Key | Default | Purpose |
|-----|---------|---------|
| `deny_cidrs` | `[]` | Extra CIDRs to block on top of the always-on SSRF list (RFC1918, loopback, link-local, CGNAT, cloud metadata, multicast, reserved, ULA). |
| `allow_ports` | `[80, 443, 8080, 8443]` | Whitelist. Use `["*"]` (or `[]`) to allow every port. SSH to port 22 → add `22`. |

**Gotcha:** `deny_cidrs` is **additive**; it never unlocks a built-in block.

## `pool`

| Key | Default | Purpose |
|-----|---------|---------|
| `strategy` | `round_robin` | Currently only `round_robin`. `least_inflight` is plumbed in the scheduler package for future use. |
| `max_retries` | `3` | Per-request worker retries on dial failure. Across different workers. |
| `backoff_ms` | `50` | Base backoff; exponential × attempt + jitter. |
| `hedge_after_ms` | `0` (off) | If set, fire a second dial on a different worker after N ms. First success wins. **Doubles** quota cost on slow dials. |
| `goroutine_size` | `0` (unbounded) | Sets an `ants` goroutine pool if >0. Useful when clients open bursts of thousands of conns. |
| `proxy_mode` | `hybrid` | `socket` / `fetch` / `hybrid`. See [proxy-modes.md](proxy-modes.md). |
| `disable_probe` | `false` | Skip the 1-byte probe after WS upgrade. Buys ~800ms per dial at the cost of losing the CF-IP fallback signal. Max throughput only. |
| `tls_rewrap` | `false` | Re-wrap the tunnel with a uTLS client (random JA3). See [tls-rewrap.md](tls-rewrap.md). |

## `rate_limit`

| Key | Default | Purpose |
|-----|---------|---------|
| `per_host_qps` | `0` (off) | Token-bucket cap per destination host. Example: `5` = 5 req/s per target. |
| `per_host_burst` | `10` | Bucket depth. Sustained rate is `per_host_qps`; short bursts up to this size pass instantly. |

Used to stop a single target from burning the whole CF quota.

## `admin`

| Key | Default | Purpose |
|-----|---------|---------|
| `addr` | `""` (disabled) | `host:port` for the admin HTTP mux. `127.0.0.1:9090` for local. |
| `token` | `""` | Bearer token. Checked as `Authorization: Bearer <token>`. |
| `api_key` | `""` | Also accepts `X-API-Key` OR `Authorization: ApiKey <key>`. Doubles as the **bootstrap login** for the web UI. |
| `basic_user` / `basic_pass` | `""` | HTTP Basic. Any auth method alone is enough; they're OR'd. |
| `enable_pprof` | `false` | Enables `/debug/pprof/*`. **Gate behind auth**; exposes runtime internals. |
| `ui` | `false` | Enables the React admin SPA at `/ui/*` and its supporting endpoints (`/apikeys`, `/test-request`, `/test-history`, `/accounts/*/pause\|resume\|deploy`, `/config/proxy-mode`, `/metrics/series`, `/audit`). See [admin-web-ui.md](admin-web-ui.md). |
| `totp_secret` | `""` | Base32 TOTP shared secret. When set, `POST /session` requires a 6-digit code in addition to the api_key (Google Authenticator / `oathtool --totp -b $SECRET`). Header-auth callers (`Bearer`, `X-API-Key`) bypass TOTP; it only gates the web-UI session login. |

If all bootstrap auth fields are empty AND no named `flx_*` key exists,
the admin HTTP is **unauthenticated**. Only acceptable bound to
`127.0.0.1`.

**Browser popup:** the 401 response uses `WWW-Authenticate: Basic` only
when `basic_user` is configured; otherwise it's `Bearer`, which doesn't
trigger a browser credential popup. Fixes the long-standing "I get a
Basic auth prompt I can't dismiss" issue when the SPA hits an expired
session.

## `state`

| Key | Default | Purpose |
|-----|---------|---------|
| `path` | `./flarex.db` | bbolt file. Buckets: `workers` (deployed Worker records) + `quota_history` (daily snapshots). |

**Gotcha:** bbolt locks the file. Two FlareX processes on the same path will
race; one will fail with `timeout`. Separate DBs per instance.

## `quota`

| Key | Default | Purpose |
|-----|---------|---------|
| `daily_limit` | `100000` | CF free-tier Workers daily quota. Set higher for paid. |
| `warn_percent` | `80` | Fire a warning alert at N% usage. `0` disables warnings. |
| `seed_from_cloudflare` | `false` | On boot, call CF GraphQL Analytics to preload the day's usage. Avoids false "0/100000" after a restart mid-day. |

When a worker account hits 100%, FlareX marks those workers as
`Healthy=false` until UTC midnight (`quotaResumeLoop` in `main.go`); traffic
routes to other accounts.

## `alerts`

| Key | Default | Purpose |
|-----|---------|---------|
| `cooldown_sec` | `900` (15 min) | Minimum gap between duplicate `(kind, account)` alerts. Prevents spam loops. |
| `http_webhooks` | `[]` | List of `{url, headers}`. POSTs JSON `{kind, account_id, message, used, limit, at}`. |
| `discord_webhook_url` | `""` | Discord webhook URL. Renders a rich embed with a progress bar. |
| `discord_username` | `FlareX` | Bot name shown in Discord. |

## `tracing` (OpenTelemetry)

| Key | Default | Purpose |
|-----|---------|---------|
| `endpoint` | `""` (disabled) | OTLP gRPC endpoint, e.g. `localhost:4317`. |
| `insecure` | `false` | `true` = plaintext gRPC (dev collectors). `false` = TLS. |

See [observability.md](observability.md).

## `timeouts`

All values are Go durations (`15s`, `800ms`, `2m`). Override any one via
`FLX_TIMEOUT_<NAME>`.

| Key | Default | What it covers |
|-----|---------|----------------|
| `dial` | `15s` | Whole WS-to-Worker dial (TCP + TLS + WS upgrade + HMAC probe). |
| `probe` | `800ms` | Post-handshake 1-byte probe in socket mode. |
| `probe_fetch` | `1500ms` | Same probe in fetch mode (CF edge adds latency). |
| `tls_handshake` | `10s` | Per-Worker HTTP/2 transport TLS step. |
| `idle_conn` | `120s` | HTTP/2 idle conn TTL. Lower = more TLS handshakes. |
| `h2_read_idle` / `h2_ping` | `30s` / `10s` | HTTP/2 keep-alive probes. |
| `health_check_interval` / `health_check_timeout` | `30s` / `5s` | Background `/__health` poll. |
| `dns_cache_ttl` | `5m` | In-process DNS cache for Worker hostnames. |
| `cf_blocked_cache_ttl` | `10m` | Remembers `host:port` that's CF-hosted (skip socket, go fetch). |
| `prewarm_attempt` / `prewarm_retries` | `3s` / `5` | Per-worker boot prewarm. |
| `breaker_interval` / `breaker_open` | `60s` / `30s` | Per-Worker circuit breaker. |
| `cf_api` | `30s` | Cloudflare REST calls. |
| `destroy_on_exit` | `60s` | Shutdown deadline when `--destroy-on-exit`. |
| `admin_shutdown` | `2s` | Graceful admin HTTP shutdown. |
| `admin_token_op` | `120s` | `/tokens` POST/DELETE runtime op. |
| `drain_timeout` / `drain_poll` | `30s` / `100ms` | Rotation drain phase: wait until old Worker has zero inflight before delete. |

## `security`

```yaml
security:
  hmac_secret: "<32+ random chars>"
```

Shared secret between the proxy and each Worker. Injected into `template.js`
at deploy time and validated on every request. **Change = redeploy all
Workers.** Prefer `FLX_HMAC_SECRET` env var in production so it never sits on
disk.

---

## Environment variables

Any `listen.socks5` / `admin.api_key` / etc. can be overridden with
`FLX_LISTEN_SOCKS5` / `FLX_ADMIN_API_KEY`. Full list:

```
FLX_CONFIG                 path to config.yaml
FLX_CONFIG_EXAMPLE_URL     where `server` downloads the default config first-run
FLX_HMAC_SECRET            security.hmac_secret (recommended)
FLX_TOKENS                 tokens[] (comma-separated)
FLX_LISTEN_SOCKS5          listen.socks5
FLX_LISTEN_HTTP            listen.http
FLX_LISTEN_AUTH_USER       listen.auth_user
FLX_LISTEN_AUTH_PASS       listen.auth_pass
FLX_ADMIN_ADDR             admin.addr
FLX_ADMIN_API_KEY          admin.api_key
FLX_ADMIN_TOKEN            admin.token
FLX_ADMIN_BASIC_USER       admin.basic_user
FLX_ADMIN_BASIC_PASS       admin.basic_pass
FLX_ADMIN_UI               admin.ui (true|false|1|yes)
FLX_ADMIN_TOTP_SECRET      admin.totp_secret (base32)
FLX_DISCORD_WEBHOOK_URL    alerts.discord_webhook_url
FLX_WORKER_COUNT           worker.count
FLX_WORKER_PREFIX          worker.name_prefix
FLX_WORKER_BACKEND         worker.deploy_backend
FLX_LOG_LEVEL              log.level
FLX_STATE_PATH             state.path
FLX_CLIENT_CONFIG          client CLI persisted config path (default ~/.config/flarex/client.yaml)
FLX_TIMEOUT_<NAME>         any of the timeouts keys, UPPERCASE_SNAKE
```

Env wins over YAML wins over defaults.

**Why env support matters:** Docker/Portainer workflows often can't mount a
YAML; they pass everything via `-e FLX_*`. See
[`deploy/docker-compose.yml`](../deploy/docker-compose.yml) for the full
suggested env surface.
