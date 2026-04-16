[← Back to docs](README.md)

# Recipes

Copy-paste recipes for every realistic FlareX workflow. Each recipe is:
**what to run · what you get · why / when / gotcha.** Outputs come from a
live bench, anonymized (account IDs, subdomains, tokens, egress IPs
redacted). Random worker-name hex (e.g. `flarex-7039b31edb87`) is left
in; it shows how CF returns it.

> This file is the **user-facing recipe book**. Thematic docs
> (`cli.md`, `admin-api.md`, `proxy-modes.md`, …) cross-link here. When
> you just want to look up a command, this is the file you want.

## Contents

- [1. Deployment lifecycle](#1-deployment-lifecycle): deploy / destroy / list / clean / seed
- [2. Server modes](#2-server-modes): flags, proxy-mode overrides, placeholder safeguards
- [3. Frontends + auth](#3-frontends--auth): SOCKS5, HTTP CONNECT, unix sockets, sticky sessions
- [4. Traffic types](#4-traffic-types): HTTPS, SSH, CF-hosted targets, `socks5h` vs `socks5`
- [5. Admin API](#5-admin-api): auth modes, /status, /metrics, /metrics/history, tokens, pprof
- [6. Operational features](#6-operational-features-rotation-drain-breaker-hedge): rotation, drain, breaker, hedge
- [7. Observability](#7-observability): log levels, JSON logs, Prometheus scrape
- [8. Security + filtering](#8-security--filtering): HMAC, SSRF, port allowlist
- [9. Edge cases + failure modes](#9-edge-cases--failure-modes): DB lock, bad token, pool unhealthy, quota 100%

---

## 1. Deployment lifecycle

### 1.5 Destroy with no workers

**Do:** Idempotent cleanup: runs even if nothing is deployed, exits clean.

**Run:**
```bash
flarex destroy
```

**Output:**
```
02:09:48 INF account resolved from token account=<account-id> name="<Your Account>" subdomain=<subdomain>

DESTROYED: nothing
```

**Notes:** Exit 0 + explicit "nothing" in the summary table. Useful as the
first step of a CI cleanup job (no preconditions). `destroy` is prefix-scoped
(`worker.name_prefix`), so it never touches workers you didn't deploy.

---

### 1.9 List empty pool

**Do:** Check no workers are deployed without opening the admin UI.

**Run:**
```bash
flarex list
```

**Output:**
```
02:09:41 INF account resolved from token account=<account-id> name="<Your Account>" subdomain=<subdomain>
no workers deployed
```

**Notes:** `list` queries the CF API live (not the local bbolt state), so
it reflects reality even if you `rm flarex.db` between runs. Exit 0.

---

### 1.10 List populated pool

**Do:** Inspect every deployed Worker: name, backend, URL, owning account.

**Run:**
```bash
flarex list
```

**Output:**
```
02:10:11 INF account resolved from token account=<account-id> name="<Your Account>" subdomain=<subdomain>
NAME                 BACKEND      ACCOUNT    URL
----                 -------      -------    ---
flarex-1e6d08944104  workers_dev  <acct>…  https://flarex-1e6d08944104.<subdomain>.workers.dev
flarex-3d8a2032da4f  workers_dev  <acct>…  https://flarex-3d8a2032da4f.<subdomain>.workers.dev
flarex-61f3425153a3  workers_dev  <acct>…  https://flarex-61f3425153a3.<subdomain>.workers.dev
02:10:12 INF workers count=3
```

**Notes:** Table is aligned via `text/tabwriter`. Account IDs truncated at
9 chars for readability (full ID still visible in the banner line).
Multi-account deployments will show different IDs across rows.

---

### 1.11 Seed a dev worker (offline)

**Do:** Register a local / fake Worker URL in state without a CF roundtrip.
Used to point FlareX at `bin/mockworker` for offline testing.

**Run:**
```bash
# Terminal 1: mock worker
MOCK_HMAC_SECRET=bench ./bin/mockworker --addr 127.0.0.1:8787

# Terminal 2:
flarex seed --name test-worker-1 --url http://127.0.0.1:8787
```

**Output:**
```
02:09:55 INF seeded name=test-worker-1 url=http://127.0.0.1:8787
```

**Notes:** `flarex list` won't show this worker (list queries CF, not state);
`flarex server` WILL load it from state and use it in the pool. Useful for
benchmarking / offline dev without burning CF quota. Delete later with `rm
flarex.db` or by running `destroy` (which only affects CF, not seeded rows).

---
### 1.8 Clean refuses empty / defaulted prefix (safety guard)

**Do:** Block a footgun where `worker.name_prefix: ""` would match (and
delete) every Worker on the account.

**Run:**
```bash
# config has: worker.name_prefix: ""
flarex -c /tmp/config-empty-prefix.yaml clean
```

**Output:**
```
02:21:33 INF account resolved from token account=<account-id> name="<Your Account>" subdomain=<subdomain>
02:21:33 FTL fatal error="worker.name_prefix was not set in config (silently defaulted to \"flarex-\"): refusing to clean; explicitly set worker.name_prefix in config.yaml to acknowledge the scope"
```

**Notes:** Guard triggers when `worker.name_prefix` is empty in YAML (even
if `applyDefaults` silently filled in `"flarex-"`). Also blocks prefixes
shorter than 3 chars. Pair `clean` with `--dry-run` on any new config;
`clean` is destructive and scoped to the whole CF account.

---
### 1.1 Deploy a fresh pool (workers_dev)

**Do:** Provision the `worker.count` Workers for each account from a clean state.

**Run:**
```bash
rm flarex.db && flarex deploy
```

**Output:**
```
02:28:17 INF account resolved from token account=<account-id> name="<Your Account>" subdomain=<subdomain>
02:28:17 INF deploying accounts=1 count_per_account=3
02:28:19 INF worker deployed backend=workers_dev name=flarex-ee4ab7b3d853 url=https://flarex-ee4ab7b3d853.<subdomain>.workers.dev
02:28:19 INF worker deployed backend=workers_dev name=flarex-177167ff1033 url=https://flarex-177167ff1033.<subdomain>.workers.dev
02:28:19 INF worker deployed backend=workers_dev name=flarex-1c453953184e url=https://flarex-1c453953184e.<subdomain>.workers.dev

DEPLOYED (3)
NAME                 BACKEND      ACCOUNT    URL
----                 -------      -------    ---
flarex-ee4ab7b3d853  workers_dev  <acct>…  https://flarex-ee4ab7b3d853.<subdomain>.workers.dev
flarex-177167ff1033  workers_dev  <acct>…  https://flarex-177167ff1033.<subdomain>.workers.dev
flarex-1c453953184e  workers_dev  <acct>…  https://flarex-1c453953184e.<subdomain>.workers.dev
```

**Notes:** `workers_dev` backend publishes to `<name>.<subdomain>.workers.dev`,
live within seconds, no DNS plumb. Each name is random hex; the
`name_prefix` (default `flarex-`) scopes every `destroy` / `clean` so
foreign Workers on the same account are safe.

---

### 1.2 Override pool size from the CLI

**Do:** Deploy N Workers without touching `config.yaml`.

**Run:**
```bash
flarex deploy -n 5
```

**Output:**
```
02:28:29 INF deploying accounts=1 count_per_account=5
02:28:31 INF worker deployed backend=workers_dev name=flarex-9a0fd48aadfd
02:28:31 INF worker deployed backend=workers_dev name=flarex-e2e65fe23f93
02:28:31 INF worker deployed backend=workers_dev name=flarex-5e4c6cb84939
02:28:31 INF worker deployed backend=workers_dev name=flarex-cbe8d88081cd
02:28:31 INF worker deployed backend=workers_dev name=flarex-25b80db12fbd

DEPLOYED (5)
```

**Notes:** `-n` overrides `worker.count` for this single invocation. Useful
for CI runs that want a bigger or smaller pool than the dev default
without editing YAML.

---

### 1.3 Re-deploy against an existing pool

**Do:** See what happens when you `deploy` twice without destroying.

**Run:**
```bash
flarex deploy   # pool already has 3
```

**Output:**
```
02:27:47 INF deploying accounts=1 count_per_account=3
02:27:50 INF worker deployed backend=workers_dev name=flarex-248c3f4676b2
02:27:50 INF worker deployed backend=workers_dev name=flarex-594ab6595283
02:27:51 INF worker deployed backend=workers_dev name=flarex-e36cad2d5089

DEPLOYED (3)
```

**Notes:** `deploy` is currently **additive, not idempotent**: the three
new Workers are added on top of the three already present (`flarex list`
shows 6). Always `destroy` (or `clean`) before a re-deploy unless you
explicitly want to scale up. See [issue in the
backlog](../README.md#known-issues) for the idempotency patch.

---

### 1.4 Destroy every Worker

**Do:** Delete all Workers matching `worker.name_prefix`.

**Run:**
```bash
flarex destroy
```

**Output:**
```
02:28:08 INF worker deleted backend=workers_dev name=flarex-1171262e1336
02:28:09 INF worker deleted backend=workers_dev name=flarex-248c3f4676b2
02:28:10 INF worker deleted backend=workers_dev name=flarex-3f3e07bead01
...

DESTROYED (6)
NAME                 BACKEND      ACCOUNT    URL
----                 -------      -------    ---
flarex-1171262e1336  workers_dev  <acct>…  https://flarex-1171262e1336.<subdomain>.workers.dev
...
```

**Notes:** Prefix-scoped: only touches Workers matching the configured
prefix, so a shared account stays safe. State DB is emptied in lock-step;
follow with `rm flarex.db` if you also want to drop orphan records from a
crashed previous run.

---

### 1.6 Preview a clean with `--dry-run`

**Do:** See which Workers + DNS records `clean` would delete, without
touching anything.

**Run:**
```bash
flarex clean --dry-run
```

**Output:**
```
02:28:01 INF account resolved from token account=<account-id> name="<Your Account>" subdomain=<subdomain>

[DRY-RUN] WORKERS TO DELETE
NAME                 ACCOUNT
----                 -------
flarex-1171262e1336  <acct>…
flarex-248c3f4676b2  <acct>…
flarex-3f3e07bead01  <acct>…
-- 3 worker(s)

no DNS records to delete
```

**Notes:** Run before every `clean` in a shared-account setting; confirms
the prefix filter matched only what you expect. The DNS-records table is
empty here because the pool is on `workers_dev`; `custom_domain` backends
would populate it.

---


## 2. Server modes

### 2.2 Auto-deploy on empty state (`server --deploy`)

**Do:** Start FlareX and deploy Workers if the local state is empty. One
command for a cold-start deployment.

**Run:**
```bash
flarex server -c config.yaml --deploy
```

**Output (abridged):**
```
      ▄▄█████▄▄       _____ _                __   __
   ▄██▀      ▀██▄    |  ___| | __ _ _ __ ___ \ \ / /
  ██▀          ▀██   | |_  | |/ _` | '__/ _ \ \ V /
 ██              ██  |  _| | | (_| | | |  __/  > <
█▀  ▄▄██████▄▄    █  |_|   |_|\__,_|_|  \___| /_/\_\
█ ▄██▀      ▀██▄  █
 ▀██          ██▀      FlareX — SOCKS5 / HTTP rotator
   ▀██▄     ▄██▀       over Cloudflare Workers
      ▀████▀
  version=0.1.0-rc commit=abc1234 built=2026-04-16T01:02:03Z

02:10:33 INF pool loaded auto_deploy=false destroy_on_exit=false pool_size=3
02:10:34 INF quota seeded from CF Analytics account=<account-id> limit=100000 seeded_with=13
────────────────────────────────────────────────────────────────
▸ Pool
  workers                 : 3 loaded across 1 account(s)
  backend                 : workers_dev
  strategy                : round_robin
  proxy_mode              : hybrid

▸ Listeners
  socks5                  : tcp://127.0.0.1:1080
  http                    : tcp://127.0.0.1:8118
  admin                   : 127.0.0.1:9090

▸ Quota
  daily_limit_per_account : 100000
  daily_limit_total       : 100000
  warn_threshold          : 80% (80000 req)
...
────────────────────────────────────────────────────────────────

02:10:34 INF FlareX server ready
02:10:34 INF SOCKS5 listening addr=tcp://127.0.0.1:1080
02:10:34 INF HTTP CONNECT frontend listening addr=tcp://127.0.0.1:8118
02:10:34 INF admin HTTP listening addr=127.0.0.1:9090 pprof=false
02:10:34 INF pool pre-warmed elapsed=49.9 workers=3
02:10:38 INF verify: template hash check done checked=3 drifted=0
```

**Notes:**
- `--deploy` is a **one-shot guard**: it deploys only if `state.path` has
  zero workers. Safe to leave on in systemd units.
- Use `--ephemeral` instead (= `--deploy --destroy-on-exit`) for ad-hoc
  scanning sessions that should leave CF clean.
- The startup banner + config block prints to **stderr**; suitable for
  journald ingest.

---
### 2.6 Force fetch-only proxy mode

**Do:** Override `pool.proxy_mode` from the CLI to make every dial use the
Worker's `fetch()` HTTP path (no raw socket attempts). Useful for CF-hosted
targets that always 4001 the socket path.

**Run:**
```bash
flarex -c config.yaml server --proxy-mode fetch
```

**Output (startup config block):**
```
▸ Pool
  workers    : 3 loaded across 1 account(s)
  backend    : workers_dev
  strategy   : round_robin
  proxy_mode : fetch
```

**Notes:** `--proxy-mode` always wins over the YAML value; the startup
banner echoes it back so you can sanity-check from the log. In `fetch`
mode raw TCP (SSH, Postgres, etc.) won't work; use `socket` or `hybrid`
for those.

---

### 2.7 Hybrid proxy mode (default, smart fallback)

**Do:** Try a raw socket first; on Cloudflare's 4001 (CF IP block), sniff
the client bytes. Promote to fetch if HTTP, close cleanly if TLS / SSH.
Best-of-both for mixed traffic.

**Run:**
```bash
flarex -c config.yaml server --proxy-mode hybrid
```

**Output:**
```
▸ Pool
  proxy_mode : hybrid
```

**Notes:** Hybrid is the default if `pool.proxy_mode` is unset; passing the
flag explicitly is useful in scripts that override a YAML which picked
`socket` or `fetch`. See [proxy-modes.md](proxy-modes.md) for the
byte-sniffing decision tree.

---

### 2.11 Server refuses to start with placeholder secret

**Do:** Fail-closed at boot if `security.hmac_secret` is still the example
value shipped in `config.example.yaml`. Prevents accidentally running
with a publicly-known secret.

**Run:**
```bash
# config has: security.hmac_secret: "change_me_32b_minimum_random_string"
flarex -c /tmp/cfg-2.11.yaml server
```

**Output:**
```
02:15:17 FTL fatal error="config /tmp/cfg-2.11.yaml still contains template placeholder \"change_me_32b_minimum_random_string\": edit it first"
```

**Notes:** Exit 1, no listeners bound, no DB writes. Same guard fires for
the `admin.api_key` placeholder. Override the secret via `FLX_HMAC_SECRET`
env var if you don't want it in the YAML at all.

---
### 2.1 Classic server (persistent, no auto-deploy)

**Do:** Start SOCKS5 + HTTP CONNECT + admin against an existing pool,
without touching CF on startup.

**Run:**
```bash
flarex server -c config.yaml
```

**Output:**
```
02:29:21 INF FlareX server ready
02:29:22 INF SOCKS5 listening addr=tcp://127.0.0.1:1080
02:29:22 INF HTTP CONNECT frontend listening addr=tcp://127.0.0.1:8118
02:29:22 INF admin HTTP listening addr=127.0.0.1:9090 pprof=false
02:29:22 INF pool pre-warmed elapsed=49.3 workers=3
```

**Notes:** The baseline mode: pool is taken from the state DB as-is.
`pool pre-warmed` confirms all Workers responded to a health probe before
traffic is accepted. Graceful shutdown via SIGTERM.

---

### 2.3 `--destroy-on-exit`

**Do:** Run a server that deletes every Worker on graceful shutdown.

**Run:**
```bash
flarex server --destroy-on-exit
# ... later ...
kill -TERM $PID
```

**Output:**
```
02:31:10 INF FlareX server ready
02:31:11 INF pool pre-warmed elapsed=539.6 workers=3
02:31:14 INF verify: template hash check done checked=3 drifted=0
02:31:16 INF destroy-on-exit: tearing down Workers
02:31:16 INF worker deleted backend=workers_dev name=flarex-461beeaf4355
02:31:16 INF worker deleted backend=workers_dev name=flarex-1171262e1336
02:31:16 INF worker deleted backend=workers_dev name=flarex-3f3e07bead01
```

**Notes:** Pairs with `--deploy` for a self-contained lifecycle. Prefer
the `--ephemeral` shortcut (2.4) for CI / ad-hoc scan jobs that must
leave CF clean. The `FTL` line on some systems is the listener shutting
down mid-accept (cosmetic, not a real error).

---

### 2.4 `--ephemeral` (auto-deploy + auto-destroy)

**Do:** Spin a fresh pool, run the server, delete everything on SIGTERM.
A throwaway CI job in one invocation.

**Run:**
```bash
flarex server --ephemeral
```

**Output:**
```
02:30:47 INF auto-deploy: no Workers in state, deploying...
02:30:49 INF pool loaded auto_deploy=true destroy_on_exit=true pool_size=3
02:30:49 INF FlareX server ready
...
02:30:54 INF destroy-on-exit: tearing down Workers
02:30:55 INF worker deleted backend=workers_dev name=flarex-229d3175ca1a
02:30:56 INF worker deleted backend=workers_dev name=flarex-52bddbf805c0
02:30:56 INF worker deleted backend=workers_dev name=flarex-eefe29c81c8b
```

**Notes:** `--ephemeral` = `--deploy + --destroy-on-exit`. Use for
short-lived scan jobs so nothing is left behind on CF. Pool is recreated
every run; no persistence needed (empty `flarex.db` OK).

---

### 2.5 `--proxy-mode socket` (no fetch fallback)

**Do:** Force socket-only dialing. Non-CF targets work; CF-hosted targets
fail cleanly.

**Run:**
```bash
flarex server --proxy-mode socket
# in another shell:
curl -x socks5h://127.0.0.1:1080 https://google.com/        # → 301 (OK, non-CF)
curl -x socks5h://127.0.0.1:1080 https://www.cloudflare.com/ # → fails (blocked)
```

**Output:**
```
  proxy_mode : socket
02:31:57 INF SOCKS5 listening addr=tcp://127.0.0.1:1080
02:31:59 WRN dial failed (all retries) error="upstream blocked (likely CF-hosted target)" port=443 target=www.cloudflare.com
```

**Notes:** `socket` mode is fastest (raw TCP via `connect()` in the
Worker) but gives up on Cloudflare-hosted targets because CF blocks
CF→CF. Use `hybrid` (default) for mixed workloads, `socket` only when
you know all your targets are off-CF.

---


## 3. Frontends + auth

### 3.1 SOCKS5 only (default loopback)

**Do:** Run FlareX with the bare-minimum `listen.socks5` and proxy a single
HTTPS request through a Cloudflare Worker.

**Run:**
```bash
flarex server -c config.yaml
# in another shell:
curl -x socks5h://127.0.0.1:1080 https://ifconfig.me
```

**Output:**
```
<egress-ipv6>
```

**Notes:** The IP returned by `ifconfig.me` is the Worker's egress (a
`2a09:bac5:…` IPv6 from Cloudflare's pool), proving the request left
through CF and not your local NIC. No HTTP CONNECT listener needed for
SOCKS5-only clients.

---

### 3.2 SOCKS5 + HTTP CONNECT dual listener

**Do:** Bind both `listen.socks5` and `listen.http` so SOCKS5-aware
clients (curl, ssh, nmap) and HTTP-CONNECT-only clients (browsers, old
Java apps) both work without an external converter.

**Run:**
```yaml
# config.yaml
listen:
  socks5: tcp://127.0.0.1:1080
  http:   tcp://127.0.0.1:8118
```

```bash
flarex server
curl -x socks5h://127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8118    https://ifconfig.me
```

**Output:**
```
<egress-ipv6>
<egress-ipv6>
```

**Notes:** Both listeners share the same worker pool: rotation,
sticky-sessions, and quota apply identically. The HTTP listener
implements **CONNECT only**; plain-HTTP forwarding (`curl -x http://…
http://target`) is not wired up. Use SOCKS5 for that.

---

### 3.3 Unix-socket SOCKS5 (no TCP exposure)

**Do:** Bind SOCKS5 to a unix socket so only processes that can `open(2)`
the path can speak to FlareX. No `127.0.0.1` listener, no `netstat`
footprint.

**Run:**
```yaml
listen:
  socks5: "unix:///tmp/flarex.sock"
  unix_perms: 0660      # or 0666 for a less-strict dev setup
```

```bash
flarex server -c config.yaml

# curl doesn't speak SOCKS5 over unix directly; socat bridges it:
socat TCP-LISTEN:11180,reuseaddr,fork UNIX-CONNECT:/tmp/flarex.sock &
curl -x socks5h://127.0.0.1:11180 https://ifconfig.me
```

**Output:**
```
srw-rw---- 1 vozec vozec 0 Apr 16 02:16 /tmp/flarex.sock
<egress-ipv6>
```

**Notes:** Prefer `0660` + a dedicated group in prod. Native
SOCKS5-over-unix clients (Go `proxy.SOCKS5("unix", path, …)`, some pentest
tools) skip the socat hop entirely.

---

### 3.4 SOCKS5 anonymous (no auth)

**Do:** Run with no `listen.auth_user` / `listen.auth_pass` to allow
unauthenticated clients on the loopback (default in
`config.example.yaml`).

**Run:**
```bash
flarex server                                         # no auth in config
curl -x socks5h://127.0.0.1:1080 https://ifconfig.me  # no creds
```

**Output:**
```
<egress-ipv6>
```

**Notes:** Safe only when bound to `127.0.0.1` / a unix socket / a trusted
LAN. The moment you publish FlareX past loopback, switch to 3.5 (user/
pass) or front it with a mTLS reverse proxy.

---

### 3.5 SOCKS5 user/password auth

**Do:** Require RFC-1929 user/pass on every SOCKS5 handshake; clients
that don't authenticate get rejected before any worker is touched.

**Run:**
```yaml
listen:
  socks5: "tcp://127.0.0.1:1080"
  auth_user: "testuser"
  auth_pass: "testpass"
```

```bash
flarex server
curl -x socks5h://testuser:testpass@127.0.0.1:1080 https://ifconfig.me
```

**Output:**
```
<egress-ipv6>
```

**Notes:** `auth_user` / `auth_pass` apply to BOTH the SOCKS5 listener and
the HTTP-CONNECT listener (same creds, different framing: Basic header
vs RFC-1929). Set `auth_pass` from `FLX_LISTEN_AUTH_PASS` env var in prod,
never inline.

---

### 3.6 HTTP CONNECT Basic auth

**Do:** Same `auth_user` / `auth_pass` as 3.5, but consumed by the
HTTP-CONNECT listener via `Proxy-Authorization: Basic …`.

**Run:**
```bash
# wrong creds → 407 Proxy Authentication Required
curl -x http://wrong:wrong@127.0.0.1:8118 https://ifconfig.me

# correct creds → request flows through a Worker
curl -x http://testuser:testpass@127.0.0.1:8118 https://ifconfig.me
```

**Output:**
```
# wrong auth -> 407
curl: (56) CONNECT tunnel failed, response 407

# correct auth -> egress IP
<egress-ipv6>
```

**Notes:** The `407` rejection is logged as `auth fail` and never reaches
the worker pool, so quota + per-host QPS counters stay untouched. Useful
when putting FlareX behind nginx with upstream `proxy_pass`.

---

### 3.7 Sticky session via SOCKS5 username

**Do:** Encode a session id inside the SOCKS5 username
(`<user>-session-<id>` or `<user>:session:<id>`) so every connection with
the same session id consistently hashes to the same Worker.

**Run:**
```bash
# config: listen.auth_user=alice / listen.auth_pass=pw
for i in 1 2 3 4; do
  curl -sS -x socks5h://alice-session-abc:pw@127.0.0.1:1080 \
       https://ifconfig.me
done
# different session id → may pick a different worker:
curl -sS -x socks5h://alice-session-XYZ:pw@127.0.0.1:1080 \
     https://ifconfig.me
```

**Output:**
```
req 1 alice-session-abc: <egress-ipv6>
req 2 alice-session-abc: <egress-ipv6>
req 3 alice-session-abc: <egress-ipv6>
req 4 alice-session-abc: <egress-ipv6>

req alice-session-XYZ:   <egress-ipv6>
```

**Notes:** Stickiness pins the **Worker**, not the **egress IP**. CF
assigns multiple IPs per Worker and rotates per-flow, so successive
requests via the same session can still appear from different IPs in a
`/64`. If the target must see a single IP, combine this with a Worker
that pins `cf.connectingIp` (out of FlareX core scope).

---

### 3.8 Auth rejection path (bad credentials)

**Do:** Verify wrong SOCKS5 creds are rejected during the sub-negotiation,
before any Worker is dialed and before any quota is consumed.

**Run:**
```bash
# server: auth_user=testuser / auth_pass=testpass
curl -x socks5h://testuser:WRONG@127.0.0.1:1080 https://ifconfig.me
```

**Output:**
```
* User was rejected by the SOCKS5 server (1 1).
curl: (97) User was rejected by the SOCKS5 server (1 1).
```

**Notes:** Reply is RFC-1929 status `0x01` (failure); FlareX closes the
TCP connection immediately. Brute-forcers pay one TCP round-trip per
attempt with no Worker dial. `cft_dial_success_total` and
`cft_connections_total` do NOT increment on failed auth.

---


## 4. Traffic types

### 4.1 HTTPS to a non-Cloudflare target (socket path)

**Do:** Tunnel HTTPS to a host that is *not* on Cloudflare. Exercises the
plain socket path with no fetch fallback.

**Run:**
```bash
curl -x socks5h://127.0.0.1:1080 https://ifconfig.me
```

**Output:**
```
<egress-ipv6>
```

**Notes:** `ifconfig.me` is fronted by a non-CF hoster, so the Worker can
dial it directly via the `connect()` runtime API. The IP you see is the
Worker's egress, not your home IP (proof the tunnel is end-to-end).

---

### 4.4 SSH over the SOCKS5 proxy (port 22)

**Do:** Carry raw TCP (SSH) through the proxy. Useful for bouncing SSH to
a server through a Cloudflare egress IP.

**Run:**
```yaml
# required: add 22 to filter.allow_ports
filter:
  allow_ports: [22, 80, 443, 8080, 8443]
```

```bash
flarex server

# banner probe (no interactive ssh):
timeout 10 nc -X 5 -x 127.0.0.1:1080 your.ssh.host 22 < /dev/null

# real interactive session:
ssh -o ProxyCommand='nc -X 5 -x 127.0.0.1:1080 %h %p' user@your.ssh.host
```

**Output (with port 22 NOT in allow_ports, default config):**
```
nc: connection failed, SOCKSv5 error: Connection not allowed by ruleset
```

**Notes:** The default config restricts `allow_ports` to `[80, 443, 8080,
8443]` for SSRF defence; port 22 is blocked with the clean SOCKSv5
"Connection not allowed by ruleset". Add `22` (or any non-HTTP port you
need) to `filter.allow_ports`. See [cloudflare-limitations.md](
cloudflare-limitations.md) for what Workers can vs can't tunnel.

---

### 4.6 `socks5h://` vs `socks5://` (DNS at proxy vs at client)

**Do:** Show the difference between the two SOCKS5 sub-modes so clients
understand DNS-leak implications and CF-hosted target behaviour.

**Run:**
```bash
# socks5h:// — hostname forwarded to proxy, Worker resolves it.
curl -x socks5h://127.0.0.1:1080 https://ifconfig.me        # OK
curl -x socks5h://127.0.0.1:1080 https://api.ipify.org      # CF-hosted

# socks5:// — client resolves locally and sends an IP to the proxy.
curl -x socks5://127.0.0.1:1080 https://api.ipify.org
```

**Output:**
```
# socks5h://  target: ifconfig.me (non-CF) — works
<egress-ipv6>

# socks5h://  target: api.ipify.org (CF-hosted)
curl: (35) Recv failure: Connection reset by peer

# socks5:// (local DNS)  target: api.ipify.org
curl: (35) Recv failure: Connection reset by peer
```

**Notes:** Prefer `socks5h://` always: avoids DNS leaks to your local
resolver. CF-hosted targets (Cloudflare-fronted sites) over **HTTPS** are
a deliberate catch-22 and reset cleanly: the socket path 4001's (CF
refuses self-loops), and the fetch byte-sniff fallback only promotes
HTTP streams. TLS ClientHellos are intentionally NOT wrapped in
`fetch()` (the Worker's fetch API cannot tunnel TLS; wrapping would
corrupt bytes). Use HTTP/80 to a CF-hosted target (recipe §4.2) to see
the fetch fallback succeed; use a non-CF target for HTTPS end-to-end
proof. See [proxy-modes.md](proxy-modes.md).

---

### 4.2 HTTP/80 to a Cloudflare-hosted target (byte-sniff → fetch fallback)

**Do:** Prove that hybrid mode falls back to `fetch()` when
the initial socket attempt is blocked.

**Run:**
```bash
curl -x socks5h://127.0.0.1:1080 http://www.cloudflare.com/
```

**Output:**
```
status=301 size=167
```

**Notes:** Initial socket dial gets 4001 (CF-hosted target), the first
bytes from the client are `GET / HTTP/1.1`, hybrid mode sees HTTP and
promotes the Worker into fetch-mode; response comes back as expected
(301 → https://). Transparent to `curl`. No TLS in flight so the Worker
can rewrap freely.

---

### 4.3 HTTPS to a Cloudflare-hosted target (clean close on TLS)

**Do:** Show what hybrid mode does when the target is CF-hosted AND the
client is speaking TLS (the one case it can't proxy).

**Run:**
```bash
curl -v -x socks5h://127.0.0.1:1080 https://www.cloudflare.com/
```

**Output:**
```
* Opened SOCKS connection from 127.0.0.1 port 47492 to www.cloudflare.com port 443
* TLSv1.3 (OUT), TLS handshake, Client hello (1):
* Recv failure: Connection reset by peer
* TLS connect error
status=000
```

**Notes:** Socket dial fails 4001; the byte-sniffer sees a TLS
ClientHello, which means fetch fallback is impossible (would require
MITM). The Worker closes cleanly, `curl` reports "connection reset".
Documented failure mode: enable `tls_rewrap` if you accept the
cert-pinning trade-off (see [tls-rewrap.md](tls-rewrap.md)).

---

### 4.5 Non-HTTP ports blocked by `allow_ports`

**Do:** Confirm the port allow-list rejects exotic destinations before
the proxy even dials.

**Run:**
```bash
# config has: filter.allow_ports: [80, 443, 8080, 8443]
curl -v -x socks5h://127.0.0.1:1080 telnet://redis.example.com:6379/
curl -v -x socks5h://127.0.0.1:1080 telnet://postgres.example.com:5432/
```

**Output:**
```
* cannot complete SOCKS5 connection to redis.example.com. (2)
* cannot complete SOCKS5 connection to postgres.example.com. (2)
```

**Notes:** SOCKS5 reply code `2` = "connection not allowed by ruleset".
Add `6379` / `5432` to `filter.allow_ports` if you need database tunnels,
but remember the Worker's outbound connect is HTTP-oriented; raw TCP
to non-web backends may still hit CF egress limits.

---

### 4.7 HTTP CONNECT frontend via `curl -x http://…`

**Do:** Drive FlareX from an HTTP-only client (curl with `-x http://`, old
Java URLConnection, browser system proxy). The HTTP CONNECT listener
handles the tunnel like a forward proxy.

**Run:**
```bash
curl -x http://127.0.0.1:8118 https://ifconfig.me
```

**Output:**
```
<egress-ipv6>
```

**Notes:** The frontend speaks **CONNECT only**; plain-HTTP forwarding
(`curl -x http://… http://target`) is not implemented. Use SOCKS5 for
plain HTTP. For mixed-client environments, enable both listeners (3.2).

---


## 5. Admin API

### 5.3 Liveness probe (unauthenticated)

**Do:** Confirm the admin server is up without presenting credentials.
Suitable for k8s liveness/readiness or a load-balancer health check.

**Run:**
```bash
curl -i http://127.0.0.1:9090/health
```

**Output:**
```
HTTP/1.1 200 OK
Date: Thu, 16 Apr 2026 00:14:20 GMT
Content-Length: 2
Content-Type: text/plain; charset=utf-8

ok
```

**Notes:** `/health` is intentionally exempt from auth so probes don't need a
key. Returns plain `ok` (no JSON), so a probe can match the body verbatim.
Use `/status` for richer pool info.

---

### 5.4 Pool snapshot via X-API-Key

**Do:** Fetch the worker pool snapshot (size, names, breaker state, error
EWMA, age) using the simplest auth header.

**Run:**
```bash
curl -H "X-API-Key: <admin-api-key>" http://127.0.0.1:9090/status
```

**Output:**
```
{"pool_size":3,"workers":[{"name":"flarex-1e6d08944104","url":"https://flarex-1e6d08944104.<subdomain>.workers.dev","account":"<account-id>","healthy":true,"breaker":"closed","inflight":0,"requests":0,"errors":0,"err_rate_ewma":0,"age_sec":253}, ...]}
```

**Notes:** Server accepts `X-API-Key: <key>` or `Authorization: ApiKey <key>`
(same effect). Pipe through `jq` for readable output. For human-friendly
tabular form, use `flarex client status` (recipe 5.17).

---

### 5.9 Prometheus metrics scrape

**Do:** Pull all FlareX runtime + Go-runtime metrics in Prometheus
exposition format for scraping.

**Run:**
```bash
curl -H "X-API-Key: <admin-api-key>" http://127.0.0.1:9090/metrics
```

**Output (truncated):**
```
cft_bytes_downstream_total 0
cft_bytes_upstream_total 0
cft_connections_active 0
cft_connections_total 6
cft_dial_fail_total 0
cft_dial_latency_seconds_bucket{vmrange="5.995e-06...6.813e-06"} 2
cft_dial_latency_seconds_bucket{vmrange="7.743e-02...8.799e-02"} 1
cft_dial_latency_seconds_sum 0.79
cft_dial_latency_seconds_count 6
cft_dial_success_total 6
cft_filter_denied_total 0
cft_handshake_fail_total 0
... (~150 more lines of go_*, process_*, cft_* series)
```

**Notes:** FlareX-specific series are prefixed `cft_`. The endpoint also
exports the full `go_memstats_*` / `process_*` set, so one scrape gives
both app + runtime visibility. Use `flarex client metrics` if you'd rather
not deal with auth headers.

---

### 5.10 Quota usage history (per account)

**Do:** Read the daily quota-snapshot time series so you can graph
subrequest usage versus the 100k/day Workers free-plan cap.

**Run:**
```bash
curl -H "X-API-Key: <admin-api-key>" \
     "http://127.0.0.1:9090/metrics/history?days=7"
```

**Output:**
```
{"account":"","days":7,"series":[{"date":"2026-04-16","account_id":"<account-id>","used":17,"limit":100000}]}
```

**Notes:** The quota snapshotter writes one row per account every ~10 min;
on a fresh DB you may see `{"series": []}` for the first interval. Add
`&account=<id>` to filter to one account when you have several. `used` is
the cumulative subrequest count Cloudflare reports for the day.

---

### 5.13 401 unauthorized response shape

**Do:** Verify the admin server rejects unauthenticated requests with a
clean, machine-detectable 401 (so clients can prompt for a key instead
of guessing).

**Run:**
```bash
curl -i http://127.0.0.1:9090/status
```

**Output:**
```
HTTP/1.1 401 Unauthorized
Content-Type: text/plain; charset=utf-8
Www-Authenticate: Basic realm="flarex", charset="UTF-8"
X-Content-Type-Options: nosniff
Content-Length: 13

unauthorized
```

**Notes:** Body is the plain string `unauthorized` (no JSON envelope). The
`WWW-Authenticate: Basic realm="flarex"` header lets browsers pop a
credential dialog and lets clients distinguish "needs auth" from "wrong
creds". `/health` is the only endpoint exempt from this challenge.

---

### 5.14 Persist credentials with `flarex client login`

**Do:** Save admin URL + API key to `~/.config/flarex/client.yaml` so
subsequent `flarex client …` commands don't need flags; verify with
`whoami`; tear it down with `logout`.

**Run:**
```bash
flarex client login --url http://127.0.0.1:9090 --api-key <admin-api-key>
flarex client whoami
flarex client logout
flarex client whoami    # fails: "not logged in"
```

**Output:**
```
02:14:39 INF client logged in file=/home/vozec/.config/flarex/client.yaml url=http://127.0.0.1:9090
url:    http://127.0.0.1:9090
auth:   api-key
file:   /home/vozec/.config/flarex/client.yaml
02:14:39 INF client logged out file=/home/vozec/.config/flarex/client.yaml
02:14:39 FTL fatal error="not logged in: run `flarex client login --url ... --api-key ...`"
```

**Notes:** `whoami` reports the auth method (`api-key`, `bearer`, `basic`,
or `none`), handy for sanity-checking which credential is in play.
`logout` deletes the file outright; after that any `flarex client` call
(other than `login`) exits non-zero with the FTL message above.

---

### 5.17 Worker pool: human table vs. machine JSON

**Do:** Show the same `/status` snapshot two ways: a column-aligned table
for humans and `--json` for `jq` / scripts.

**Run:**
```bash
flarex client status            # tabular
flarex client status --json     # raw JSON (pipe to jq / python -m json.tool)
```

**Output (tabular):**
```
NAME                 HEALTH  BREAKER  INFLIGHT  REQ  ERR  ERR%   AGE
----                 ------  -------  --------  ---  ---  ----   ---
flarex-45d201ebe25e  ok      closed   0         1    0    0.0%   23s
flarex-71cdab8abd1c  ok      closed   0         2    0    0.0%   23s
flarex-27c26e3daa5d  ok      closed   0         1    0    0.0%   22s

pool_size=3
```

**Output (JSON):**
```json
{
  "pool_size": 3,
  "workers": [
    {
      "account": "<account-id>",
      "age_sec": 22,
      "breaker": "closed",
      "err_rate_ewma": 0,
      "errors": 0,
      "healthy": true,
      "inflight": 0,
      "name": "flarex-27c26e3daa5d",
      "requests": 1,
      "url": "https://flarex-27c26e3daa5d.<subdomain>.workers.dev"
    }
  ]
}
```

**Notes:** Table view rounds `err_rate_ewma` to one decimal and converts
`age_sec` to `Nm`/`Ns`. JSON view is the raw `/status` payload; feed it
to `jq '.workers[] | select(.healthy==false) | .name'` to list down
workers. `pool_size=` summary line is only in the table view; in JSON it's
the top-level `pool_size` key.

---
### 5.1 Bearer token auth

**Do:** Protect the admin endpoints with a plain Bearer token.

**Run:**
```bash
# config: admin.token: "bearer-secret-abc"
curl http://127.0.0.1:9090/status                                          # → 401
curl -H "Authorization: Bearer bearer-secret-abc" http://127.0.0.1:9090/status
curl -H "Authorization: Bearer wrong-token" http://127.0.0.1:9090/status   # → 401
```

**Output:**
```
# WITHOUT auth (expect 401)
status=401

# WITH Bearer
{"pool_size":3,"workers":[{"name":"flarex-5f6bfa75330a", ...}]}

# WITH wrong Bearer
status=401
```

**Notes:** The three auth modes (`admin.api_key` → `X-API-Key`,
`admin.token` → Bearer, `admin.basic_user/pass` → Basic) can be enabled
together; any valid credential passes. Use Bearer for scraper systems
that already sign their calls that way.

---

### 5.2 HTTP Basic auth

**Do:** Allow browser / legacy tools to hit the admin API with a
username+password.

**Run:**
```bash
# config: admin.basic_user: "admin", admin.basic_pass: "pass123"
curl -u admin:pass123 http://127.0.0.1:9090/status
curl -u admin:wrongpass http://127.0.0.1:9090/status   # → 401
```

**Output:**
```
# WITH correct Basic
{"pool_size":3,...}

# WITH wrong Basic
status=401

# 401 with WWW-Authenticate
HTTP/1.1 401 Unauthorized
Www-Authenticate: Basic realm="flarex", charset="UTF-8"
```

**Notes:** The `WWW-Authenticate: Basic` header triggers the browser
prompt automatically; useful for a quick web-pinnable status page. Pair
with TLS termination (caddy / nginx) since Basic is plaintext.

---

### 5.5 `POST /tokens` (runtime add a CF account)

**Do:** Add a CF token to a running server and auto-deploy a worker
batch.

**Run:**
```bash
curl -X POST -H "Authorization: Bearer bearer-secret-abc" \
     -H "Content-Type: application/json" \
     -d '{"token":"cfut_<redacted-token>"}' \
     http://127.0.0.1:9090/tokens
```

**Output:**
```
# pool before: pool_size=3
{"deployed_workers":["flarex-2115c102809e","flarex-a666bf8ee12d","flarex-1158be7b420c"]}
# pool after: pool_size=6
```

**Notes:** The endpoint deploys `worker.count` Workers (server-side
config) for the account discovered from the token. **Body `count` is
currently ignored**; if you need a different size, change
`worker.count` in config or wait for the API to honour it. Safe for
adding a second CF account to a running proxy.

---

### 5.6 `DELETE /tokens?account=...` (runtime remove)

**Do:** Drop every Worker that belongs to a given CF account without
restarting.

**Run:**
```bash
curl -X DELETE -H "Authorization: Bearer bearer-secret-abc" \
     "http://127.0.0.1:9090/tokens?account=<account-id>"
```

**Output:**
```
# pool before: pool_size=6
{"removed_workers":["flarex-5f6bfa75330a","flarex-f2b46883a9ab","flarex-fdd67e2b3c9e","flarex-2115c102809e","flarex-a666bf8ee12d","flarex-1158be7b420c"]}
# pool after: pool_size=0
```

**Notes:** Removes Workers from Cloudflare **and** from the local pool in
one call. Also supports `?token=<cftoken>` to scope to a specific token.
Use when one CF account runs out of quota and you want to shed it until
reset.

---

### 5.7 `/debug/pprof/heap` (memory profile)

**Do:** Grab a live heap profile and analyze it with `go tool pprof`.

**Run:**
```bash
# config: admin.enable_pprof: true
curl -H "Authorization: Bearer bearer-secret-abc" \
     http://127.0.0.1:9090/debug/pprof/heap -o /tmp/heap.bin
go tool pprof -top -nodecount=5 /tmp/heap.bin
```

**Output:**
```
File: flarex
Type: inuse_space
Showing top 5 nodes out of 85
      flat  flat%   sum%        cum   cum%
    3078kB 42.86% 42.86%     3078kB 42.86%  runtime.mallocgc
  514.38kB  7.16% 50.03%   514.38kB  7.16%  compress/flate.NewReader
     514kB  7.16% 57.15%      514kB  7.16%  bufio.NewWriterSize
```

**Notes:** Only exposed when `admin.enable_pprof: true`. All pprof verbs
(`/heap`, `/goroutine`, `/profile?seconds=30`, `/trace`) share the same
auth rule. Leave off in prod unless actively profiling; pprof leaks
internal state.

---

### 5.8 `/workers/{name}/logs` (Cloudflare tail SSE)

**Do:** Stream the selected Worker's live `console.log` + incoming request
headers.

**Run:**
```bash
curl -N -H "Authorization: Bearer bearer-secret-abc" \
     http://127.0.0.1:9090/workers/flarex-18812c694b8d/logs
# client stays connected; Ctrl-C to stop.
```

**Output:**
```
data: {"wallTime":59,"cpuTime":2,"outcome":"ok","scriptName":"flarex-18812c694b8d",
"logs":[],"eventTimestamp":1712345678000,
"event":{"request":{"url":"https://flarex-18812c694b8d.<subdomain>.workers.dev/?h=google.com&m=socket&p=443&...",
"method":"GET","headers":{"cf-connecting-ip":"<egress-ipv6>","cf-ipcountry":"FR","cf-ray":"<cf-ray>"}}}}
... (stream stays open)
```

**Notes:** Proxies CF's Tail API as plain `text/event-stream`, one JSON
blob per event. URLs include HMAC-signed query params; treat the stream
as sensitive. Generate traffic against the Worker in a second shell to
see events; an idle pool tails nothing.

---



## 6. Operational features (rotation, drain, breaker, hedge)

### 6.1 Hedged dial (tail-latency kicker)

**Do:** Lower `hedge_after_ms` and watch the tail-latency safety net in
action.

**Run:**
```yaml
pool:
  hedge_after_ms: 50
```

```bash
flarex server
for i in {1..10}; do curl -x socks5h://127.0.0.1:1080 https://google.com/ ; done
```

**Output:**
```
# baseline metrics (pre-traffic)
cft_dial_success_total = 0

# fire 10 sequential HTTPS requests → all succeed
curl results: success=10 fail=0

# post-traffic metrics
cft_dial_success_total = 10
cft_connections_total  = 10
```

**Notes:** On a healthy workers_dev pool most dials complete in <50 ms,
so the hedge rarely fires (success counter matches connection counter
1:1). On a slow pool you'd see `cft_dial_success_total >
cft_connections_total`: hedge losers still count as successful dials,
just discarded. Leave default 150 ms for prod; drop to 50 ms only when
chasing P99s.

---

### 6.2 Health exclusion on a dead worker

**Do:** Seed an unreachable Worker URL, hit the proxy repeatedly, watch
the pool skip it.

**Run:**
```bash
flarex seed --name flarex-dead-42 \
            --url https://definitely-not-a-real-host.example.com \
            --account <account-id>
flarex server
for i in {1..10}; do curl -x socks5h://127.0.0.1:1080 https://google.com/ ; done
```

**Output:**
```
# pool state at boot
"pool_size": 1
"workers": [{"name":"flarex-dead-42","healthy":true,"breaker":"closed"}]

# after 10 requests
"healthy": false
"breaker": "closed"        <-- breaker didn't trip; excluded by health probe first
"err_rate_ewma": 0.51

# log excerpt
WRN dial failed (all retries) error="no workers available (all unhealthy or breaker open)" target=google.com
```

**Notes:** The background health probe excludes the dead Worker before
the breaker gets a chance to see failing dials; observable state is
`healthy:false, breaker:closed`. The breaker's Open state is only
reachable when a Worker passes health checks and then starts failing
live traffic (e.g. CF region outage).

---

### 6.3 Rate limit enforcement (`per_host_qps`)

**Do:** Set an aggressive per-host rate limit and watch the token bucket
serialise bursts.

**Run:**
```yaml
rate_limit:
  per_host_qps: 2
  per_host_burst: 15
```

```bash
flarex server
for i in {1..20}; do curl -x socks5h://127.0.0.1:1080 https://google.com/ & done ; wait
```

**Output:**
```
# results (HTTP status : wall time in seconds)
301:0.092        # first 15 absorbed by burst bucket (~100 ms each)
301:0.093
...
301:0.107
301:0.606        # 16th waits for token refill (500 ms/token @ 2 qps)
301:1.099
301:1.638
301:2.099
301:2.735        # 20th
```

**Notes:** FlareX throttles **silently**: the proxy blocks until a
token is available; no `429` is returned. Bursts up to `per_host_burst`
go through instantly, then overflow requests serialise at
`1 / per_host_qps` seconds apart. Useful for staying under WAF
thresholds; set `per_host_qps: 0` to disable.

---

### 6.6 Template drift → auto-recycle

**Do:** Edit the Worker template on disk, rebuild, restart the server,
watch the pool detect + redeploy the stale Workers.

**Run:**
```bash
sed -i '1i// drift test 6.6' internal/worker/template.js
make rebuild
pkill -TERM flarex
flarex server -c config.yaml
```

**Output:**
```
02:38:29 INF verify: template drift, recycling have=39741266a0cd want=e879bdcb94e5 worker=flarex-264eb5023294
02:38:29 INF verify: template drift, recycling have=39741266a0cd want=e879bdcb94e5 worker=flarex-5c0082fc68c3
02:38:29 INF verify: template drift, recycling have=39741266a0cd want=e879bdcb94e5 worker=flarex-9cfa0fb8437c
02:38:29 INF verify: template hash check done checked=3 drifted=0
```

**Notes:** On startup FlareX hashes the embedded template and fetches
the deployed `have` hash per Worker. Mismatch logs `verify: template
drift, recycling have=<old> want=<new>` and the rotation loop swaps the
Worker in-place on its next tick (no dropped connections). Works
automatically after any `make rebuild`; no manual redeploy needed.

---


## 7. Observability

### 7.1 Debug-level logs

**Do:** Flip `log.level: debug` to see per-request tunnel + worker-selection
decisions.

**Run:**
```yaml
log:
  level: debug
```

```bash
flarex server
curl -x socks5h://127.0.0.1:1080 https://google.com/
```

**Output:**
```
02:39:08 INF FlareX server ready
02:39:11 DBG tunnel up mode=socket port=443 req_id=26e46b2818bd target=google.com worker=flarex-879dc920d037
02:39:11 DBG tunnel closed req_id=26e46b2818bd total=131.7 worker=flarex-879dc920d037
```

**Notes:** At `debug` you get `DBG tunnel up` / `DBG tunnel closed` per
request with mode, target, selected worker, and elapsed ms. Diagnose
"which Worker handled my request"; flip back to `info` for prod (debug
is verbose under load).

---

### 7.2 Structured JSON logs

**Do:** Emit logs as one JSON object per line for ingestion by Loki /
Vector / Elastic.

**Run:**
```yaml
log:
  json: true
```

```bash
flarex server 2>&1 | jq .
```

**Output:**
```json
{"level":"info","pool_size":3,"time":"2026-04-16T02:39:23+02:00","message":"pool loaded"}
{"level":"info","account":"<account-id>","seeded_with":752,"limit":100000,"time":"2026-04-16T02:39:24+02:00","message":"quota seeded from CF Analytics"}
{"level":"info","time":"2026-04-16T02:39:24+02:00","message":"FlareX server ready"}
{"level":"info","addr":"tcp://127.0.0.1:1080","time":"2026-04-16T02:39:24+02:00","message":"SOCKS5 listening"}
{"level":"info","elapsed":47.26,"workers":3,"message":"pool pre-warmed"}
```

**Notes:** Every log line is a single JSON object with `level`, `time`,
`message` plus context fields. The startup banner (`▸ Pool`, `▸
Listeners`, …) stays plain text; it's written to stderr before the
logger swaps to JSON mode, so grep past the banner before piping to
`jq`.

---

### 7.3 Prometheus metrics scrape

**Do:** Pull the first-class FlareX metrics from `/metrics`.

**Run:**
```bash
curl -s -H "X-API-Key: <admin-api-key>" http://127.0.0.1:9090/metrics | grep '^cft_'
```

**Output:**
```
cft_connections_active 1
cft_connections_total 5
cft_dial_fail_total 0
cft_dial_success_total 5
cft_dial_latency_seconds_bucket{vmrange="4.642e-02...5.275e-02"} 4
cft_dial_latency_seconds_sum 0.26
cft_dial_latency_seconds_count 5
cft_request_duration_seconds_sum 0.42
cft_request_duration_seconds_count 4
cft_worker_requests_total{name="flarex-879dc920d037"} 2
cft_worker_requests_total{name="flarex-bbb383fd01f1"} 2
cft_worker_requests_total{name="flarex-f6a018898d4b"} 1
```

**Notes:** Key series: `cft_connections_total` (accepted client
connections), `cft_dial_success_total`/`cft_dial_fail_total` (per-dial
outcome, includes hedge losers), `cft_dial_latency_seconds` (histogram
for alerting on tail regressions), `cft_worker_requests_total{name=...}`
(per-worker fanout, spot uneven load-balancing). Admin endpoint
requires auth even for `/metrics`; point Prometheus at a
`bearer_token_file` or custom-header config.

---


## 8. Security + filtering

### 8.1 Boot refuses an empty HMAC secret

**Do:** Refuse to start when `security.hmac_secret` is empty. The proxy ↔
Worker handshake is HMAC-signed; a missing secret would silently disable
the auth check.

**Run:**
```bash
# config has: security.hmac_secret: ""
flarex -c /tmp/cfg-8.1.yaml server
```

**Output:**
```
02:15:25 FTL fatal error="hmac_secret required (config or env FLX_HMAC_SECRET)"
```

**Notes:** Distinct from the placeholder check (2.11); this one catches
an empty value, not the well-known template string. Either set
`security.hmac_secret` in YAML or export `FLX_HMAC_SECRET=<32+ chars>`
before launching.

---

### 8.2 HMAC mismatch is rejected by the Worker

**Do:** Verify a server started with the wrong `security.hmac_secret`
cannot use Workers deployed under a different secret. The Worker rejects
the WebSocket upgrade.

**Run:**
```bash
# Bench server running with hmac_secret=A
# Start a second server pointing at the same Workers but with hmac_secret=B
flarex -c /tmp/cfg-8.2.yaml server &
curl -x socks5://127.0.0.1:11084 https://example.com/
```

**Output (client):**
```
curl: (97) cannot complete SOCKS5 connection to example.com. (3)
```

**Server log:**
```
WRN dial failed (all retries) error="ws dial worker: failed to WebSocket dial: expected handshake response status code 101 but got 404"
```

**Notes:** The Worker returns 404 on the upgrade because the HMAC check
rejects the auth query params. Client sees a clean SOCKS5 reply 3
(network unreachable). Rotate the secret with `flarex deploy` (re-uploads
templates) if you need to change it for real.

---

### 8.4 RFC1918 target denied before the dial

**Do:** Confirm the SSRF filter blocks private / loopback CIDRs at the
SOCKS5 frontend. The Worker never sees the request.

**Run:**
```bash
curl -x socks5://127.0.0.1:1080 http://192.168.1.1:80/
```

**Output:**
```
curl: (97) cannot complete SOCKS5 connection to 192.168.1.1. (2)
```

**Notes:** SOCKS5 reply code `2` = "connection not allowed by ruleset".
The default SSRF filter (RFC1918 + loopback + 169.254/16 link-local +
cloud metadata IPs) is always on; `filter.deny_cidrs` **adds** to it,
can't disable the built-ins.

---

### 8.5 `allow_ports` blocks unlisted destination ports

**Do:** Restrict outbound traffic to a port allowlist. A SOCKS5 dial to
anything else is refused before the Worker is contacted.

**Run:**
```bash
# config: filter.allow_ports: [80, 443, 8080, 8443]
curl -x socks5://127.0.0.1:1080 http://example.com:22/
```

**Output:**
```
curl: (97) cannot complete SOCKS5 connection to example.com. (2)
```

**Notes:** Same SOCKS5 reply `2` ("not allowed") as the CIDR filter;
clients can't tell the two cases apart, by design. Set
`filter.allow_ports: ["*"]` (or `[]`) to allow every port (e.g. for SSH).
Check happens pre-DNS; safe at high QPS.

---


## 9. Edge cases + failure modes

### 9.1 Two servers on the same DB: second one fails fast

**Do:** Prevent two `flarex server` instances from corrupting the bbolt
state by exclusive file lock on `state.path`.

**Run:**
```bash
# bench server already running with state.path=./flarex.db
flarex -c /tmp/cfg-9.1.yaml server     # same state.path
```

**Output:**
```
02:16:12 FTL fatal error="open ./flarex.db: timeout"
```

**Notes:** Exit 1, no listeners bound. Use a different `state.path` (e.g.
`/tmp/flarex-test.db`) for sandboxed servers. Same lock applies to
`flarex deploy` / `destroy` while a server is running; that's the
expected coordination.

---

### 9.4 Invalid CF token fails with a clear error

**Do:** Surface CF API rejection at the first call (token discovery)
instead of failing later in deploy and leaving a partial pool.

**Run:**
```bash
# config: tokens: ["cfut_invalid_token_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"]
flarex -c /tmp/cfg-9.4.yaml deploy
```

**Output:**
```
02:16:19 FTL fatal error="discover token: list accounts: cf api: 9109 Invalid access token"
```

**Notes:** Exit 1, nothing deployed, no DB writes. CF error code `9109`
is what you get for a malformed / wrong token; expired-but-formerly-valid
tokens come back as `1000` or `9106`. Always keep the CF code in the
message; it's the fastest way to triage at the CF dashboard.

---

### 9.6 Zero healthy workers → graceful SOCKS5 close

**Do:** When every worker in the pool is unhealthy (failed health probe,
breaker open, unreachable), the SOCKS5 frontend rejects new dials with a
clean reply code instead of hanging or crashing.

**Run:**
```bash
flarex -c /tmp/cfg-9.6.yaml seed --name dead-worker-1 --url http://127.0.0.1:1
flarex -c /tmp/cfg-9.6.yaml server &
curl -x socks5://127.0.0.1:11086 https://example.com/
```

**Output:**
```
curl: (97) cannot complete SOCKS5 connection to example.com. (3)
```

**Server log:**
```
WRN dial failed (all retries) error="no workers available (all unhealthy or breaker open)"
```

**Notes:** SOCKS5 reply `3` = "network unreachable", which `curl`
translates to error 97. The dial path exhausts `pool.max_retries`
(default 3) cycling through unhealthy workers, then bails. Monitor with
`flarex client status`; the warning is logged but the CLI exit code
from `curl` is the only client-visible signal.

---

### 9.7 Quota at 100% across accounts → clean close

**Do:** When the daily quota is exhausted (or seeded to look so at boot),
the proxy refuses new dials cleanly rather than blowing through the limit
and getting account-throttled by Cloudflare.

**Run:**
```bash
# config: quota.daily_limit: 1  (seeded_with=107 from CF Analytics → over)
flarex -c /tmp/cfg-9.7.yaml server &
curl -x socks5://127.0.0.1:11087 https://example.com/
```

**Output:**
```
curl: (97) cannot complete SOCKS5 connection to example.com. (3)
```

**Server log:**
```
INF quota seeded from CF Analytics account=<account-id> limit=1 seeded_with=107
WRN dial failed (all retries) error="no workers available (all unhealthy or breaker open)"
```

**Notes:** Outcome matches 9.6 (clean SOCKS5 reply 3). The underlying
path is the health-check (not an explicit "quota paused" state
transition; that's a polish item tracked separately). Pair quota alerts
with `alerts.discord_webhook_url` so you don't have to watch the log.

---

