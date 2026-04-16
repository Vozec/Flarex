[← Back to docs](README.md)

# CLI reference

```
flarex [--config PATH] <command> [flags]
```

Default config path: `config.yaml` · override via `-c`/`--config` or
`FLX_CONFIG`. See [configuration.md](configuration.md).

> Looking for copy-paste examples? → **[recipes.md](recipes.md)** has a
> working invocation + anonymized output for every command listed below.

## Deploy / destroy / list

### `flarex deploy`
Deploy `worker.count` Workers per account declared in `accounts[]` or resolved
from `tokens[]`. Worker names are `<prefix><random-hex>`; records land in bbolt
state. Skips accounts that already have workers matching prefix to avoid
duplicates.

**When to use:** one-time setup, or to manage the Worker lifecycle
separately from the proxy process. Pair with `server` (no `--ephemeral`)
so workers survive restarts.

**Gotchas:**
- Hitting the Workers quota (100/account on free tier) aborts partial.
- `workers_dev` backend requires a subdomain (auto-created if absent, but
  that's an async CF operation; the first deploy may retry once).

### `flarex destroy`
Delete every Worker whose name starts with `worker.name_prefix`. Scans each
configured account. Also removes bound custom-domain DNS records if
`deploy_backend` was `custom_domain`.

Safe to run repeatedly (idempotent).

### `flarex clean [--dry-run]`
Same as `destroy` but also sweeps **orphan DNS records** on your CF zones that
match the prefix (in case a previous destroy died mid-flight). `--dry-run`
lists what would be removed.

### `flarex list [--json]`
Pretty-prints every deployed Worker as an aligned table (NAME, BACKEND,
ACCOUNT, URL). Reads directly from the CF API, not local state, so it works
when the DB is out of sync. `--json` emits machine-readable output. See
[recipes.md §1.10](recipes.md#110-list-populated-pool).

### `flarex version`
Prints the binary version, commit sha, and build date (all injected at link
time via `-ldflags`). Useful for bug reports. Defaults to `dev` when built
outside of `make build` or without a git tag.

### `flarex config validate`
Loads `config.yaml`, resolves timeouts + applies env overrides, runs the
same validators that `deploy` / `server` use, then exits. No network
calls, no state DB touched. CI-safe.

## State backup / restore

### `flarex backup --out path.db`
Writes a consistent snapshot of `state.path` to a standalone bbolt file.
Uses a read transaction (no writer block) so it's safe to run while
`flarex server` is up. The output file is itself a valid bbolt DB you can
open with any bolt tool, or restore later.

```bash
flarex backup --out /backups/flarex-$(date +%Y%m%d).db
```

### `flarex restore --in path.db [--force]`
Validates that the snapshot opens cleanly, then replaces `state.path`
with it. Refuses if the destination already exists unless `--force` is
passed; with `--force`, the existing file is renamed to `<path>.bak` so
you can revert.

**Stop the server before restoring.** bbolt's exclusive lock prevents
two processes touching the same file.

```bash
systemctl stop flarex
flarex restore --in /backups/flarex-20260415.db --force
systemctl start flarex
```

## Server

### `flarex server` (alias: `serve`)
Starts:

- SOCKS5 listener on `listen.socks5`.
- HTTP CONNECT listener on `listen.http` if configured.
- Admin HTTP on `admin.addr` if configured.
- Health-check loop, quota snapshot loop, template-drift verifier, keep-alive
  loop (HTTP/2 warmth to every Worker).

**Flags:**

| Flag | Purpose |
|------|---------|
| `--deploy` | If state DB is empty, deploy before serving. |
| `--destroy-on-exit` | Delete every Worker on graceful shutdown. |
| `--ephemeral` | Shortcut: `--deploy --destroy-on-exit`. |
| `--proxy-mode MODE` | Override `pool.proxy_mode` (socket/fetch/hybrid). |
| `--config-example-url URL` | First-run bootstrap: download example config. |

**Signal handling:** SIGINT/SIGTERM → close listeners → drain inflight
connections → optionally destroy workers (timeout = `timeouts.destroy_on_exit`,
default 60s) → exit.

## `flarex seed --name N --url U`
Test helper: adds a fake Worker record to state without talking to CF. Use
with `cmd/mockworker` for local benchmarks.

## Remote admin: `flarex client …`

Used to drive a running `flarex server` instance remotely via its admin HTTP.
Credentials persisted in `~/.config/flarex/client.yaml` (override with
`FLX_CLIENT_CONFIG`).

### `client login --url URL <auth>`
Choose ONE auth method; they're checked server-side in this order (Bearer
first, then X-API-Key, then Basic):

```bash
flarex client login --url http://srv:9090 --bearer XXX
flarex client login --url http://srv:9090 --api-key XXX
flarex client login --url http://srv:9090 --user U --pass P
```

### `client whoami | logout`
Read / clear persisted credentials.

### Operations

| Command | HTTP call | Use |
|---------|-----------|-----|
| `client status [--json] [--account=ID]` | `GET /status` | Tabular worker snapshot by default; `--json` for scripting; `--account` filters. See [recipes.md §5.17](recipes.md#517-worker-pool-human-table-vs-machine-json). |
| `client metrics` | `GET /metrics` | Raw Prometheus text; pipe to `grep cft_`. |
| `client metrics-history [--days=N] [--account=ID] [--json]` | `GET /metrics/history` | Daily quota snapshots as a table; `--json` for scripting. [recipes.md §5.10](recipes.md#510-quota-usage-history-per-account). |
| `client health` | `GET /health` | Liveness probe. |
| `client add-token --token TOK` | `POST /tokens` | Discover account + deploy workers at runtime. |
| `client remove-token --account ID` | `DELETE /tokens?account=ID` | Drain + delete all workers on that account. |
| `client remove-token --token TOK` | `DELETE /tokens?token=TOK` | Same by token. |

See [admin-api.md](admin-api.md) for the raw HTTP contract.

## Exit codes

- `0` : clean shutdown (server received SIGINT/SIGTERM).
- `1` : config load / validation failure.
- `2` : bootstrap downloaded fresh config; edit + re-run.
- other non-zero : runtime failure (look at the last log line).
