[← Back to docs](README.md)

# Deployment

How to run FlareX in production and how Workers end up deployed on the CF
side.

> Verified recipes: [1.5 destroy idempotent](recipes.md#15-destroy-with-no-workers) · [1.9 list empty](recipes.md#19-list-empty-pool) · [1.10 list populated](recipes.md#110-list-populated-pool) · [1.11 seed](recipes.md#111-seed-a-dev-worker-offline) · [2.2 --deploy server flag](recipes.md#22-auto-deploy-on-empty-state-server---deploy).

## Backends

`worker.deploy_backend` decides where your Workers get attached on the CF
side.

### `workers_dev`

Deploys to `https://<name>.<subdomain>.workers.dev`. No custom
domain needed. CF auto-provisions a `.workers.dev` subdomain for every
account; FlareX discovers or creates one on first deploy.

**Pros:** zero setup, free tier, no zone ownership required.
**Cons:** URLs are obvious (`flarex-abc123.<subdomain>.workers.dev`), and
all Workers on the same account share the subdomain.

### `custom_domain`

Deploys on your own domain like `https://<name>.proxy.example.com`. Requires:

1. Your domain on CF (orange-clouded).
2. CF token with `Zone.DNS: Edit` on that zone.
3. A CNAME record created automatically by FlareX for each Worker.

**Pros:** URL looks like yours, not CF's. Per-zone isolation.
**Cons:** adds a DNS record per Worker (count x accounts), which may
trigger CF's DNS record limits on free plans.

### `auto`

Tries `custom_domain` for any zone the token has access to; falls back to
`workers_dev` if no zones or no DNS scope.

Default.

## Deploy flow

```
flarex deploy
  → config.Load()
  → for each account in cfg.Accounts + discovered(cfg.Tokens):
      → backend.Pick(mode, accountID, token, subdomain)
      → for i in worker.count:
          → render template (embed HMAC secret + template hash)
          → CF API: PUT /accounts/<id>/workers/scripts/<name>
          → enable workers.dev OR create CNAME
          → store in state.db
```

Parallel across workers within an account (`errgroup`). Serial across
accounts (avoids one account's CF API errors breaking another's deploy).

Deploy is **idempotent by prefix**: rerunning `flarex deploy` when workers
with prefix already exist skips them. To force a full rebuild: `flarex
destroy && flarex deploy`.

## Rotation

Enable by setting any of:

```yaml
worker:
  rotate_interval_sec: 300    # check every 5 min
  rotate_max_age_sec: 3600    # recycle after 1h
  rotate_max_req: 50000       # recycle after 50k req
```

Rotation loop every `rotate_interval_sec`:

1. For each Worker, check `age > max_age` OR `requests > max_req`.
2. For each stale Worker: **graceful drain**.
   - Mark `Healthy = false` so new dials skip it.
   - Poll `Inflight.Load()` until 0 or timeout
     (`timeouts.drain_timeout`, default 30s).
   - Deploy replacement under a new random name.
   - Atomically swap in the pool.
   - Delete old Worker from CF.
3. Update state DB.

Zero dropped connections as long as drain timeout is long enough.

## Template hash drift

FlareX embeds `SHA256(template.js)` at deploy time. On boot, the server
queries `/__health` on every Worker; the Worker returns its hash in
`X-Template-Hash`. If the local hash differs (you bumped the template and
rebuilt FlareX but didn't redeploy), the Worker is recycled automatically.

No manual "redeploy after template change" needed. Works for both embedded
and custom `template_path`.

## Docker / Compose

See [`deploy/docker-compose.yml`](../deploy/docker-compose.yml).

```bash
# Secrets via env:
cat > .env <<EOF
FLX_HMAC_SECRET=change_me_32b_random
FLX_TOKENS=cf_token_1,cf_token_2
FLX_ADMIN_API_KEY=change_me
EOF

docker-compose up -d
```

The compose:
- Mounts `./config.yaml` as read-only.
- Stores bbolt state in a named volume `flarex-state`.
- Exposes `:1080` (SOCKS5) + `:9090` (admin).
- Healthcheck hits `GET /health`.

**Gotcha:** `bbolt` locks the file. If you restart the container with
`--force-recreate`, the old lock is released; if the container is
killed (`docker kill`), the lock may stick. Add `rm -f flarex.db.lock`
to the entrypoint if you hit that.

## systemd

```ini
# /etc/systemd/system/flarex.service
[Unit]
Description=FlareX Cloudflare Workers proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=flarex
Group=flarex
WorkingDirectory=/var/lib/flarex
EnvironmentFile=-/etc/flarex/env
ExecStart=/usr/local/bin/flarex server -c /etc/flarex/config.yaml
Restart=on-failure
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

See [`deploy/flarex.service`](../deploy/flarex.service).

## Bare-metal tips

- **File descriptors:** `LimitNOFILE=65536` in systemd (or `ulimit -n 65536`
  in a shell). Each inflight tunnel = 2 fds.
- **Binding low ports:** if you want `listen.socks5: "tcp://:1080"` on port
  <1024, either use `setcap cap_net_bind_service=+ep /usr/local/bin/flarex`
  or reverse-proxy through something that owns the port (socat, haproxy).
- **Log rotation:** use `systemctl cat flarex` logs via journald; FlareX
  itself writes to stderr, not files.

## Multi-region

One FlareX instance per region, each with its own pool of CF tokens:

```
┌─ flarex-eu (Frankfurt) ─ EU clients
├─ flarex-us (Virginia)  ─ US clients
└─ flarex-ap (Singapore) ─ APAC clients
```

Workers egress from the CF PoP nearest the FlareX process, so this
geographically distributes your egress IPs. Each instance runs independently
with its own state DB; they don't coordinate.
