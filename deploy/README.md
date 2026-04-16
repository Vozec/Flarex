[← Back to README](../README.md)

# Production deployment

> **When to read this**: you want to run FlareX as a long-lived service via
> Docker Compose / Portainer or a systemd unit. For local test / CI runs,
> see [`test/deploy/`](../test/deploy/README.md) instead.

Production-ready scaffolding for FlareX.

| File | Use |
|------|-----|
| `docker-compose.yml` | Portainer-friendly stack — config via env vars |
| `config.yaml` | minimal stub mounted into the container |
| `flarex.service` | systemd unit (hardened) |

## Docker / Portainer

The compose stack reads everything from environment variables (no
hand-editing of YAML required). Set them in Portainer's "Environment"
section or in a local `.env` file next to the compose.

### Required env

| Var | Purpose |
|-----|---------|
| `FLX_HMAC_SECRET` | 32+ random chars; shared with deployed Workers |
| `FLX_TOKENS` | comma-separated CF API token(s); accounts auto-discovered |

### Common optional env

| Var | Default | Purpose |
|-----|---------|---------|
| `FLX_LISTEN_SOCKS5` | `tcp://0.0.0.0:1080` | SOCKS5 listen address |
| `FLX_LISTEN_AUTH_USER` / `..._PASS` | (off) | SOCKS5 client auth |
| `FLX_ADMIN_ADDR` | `0.0.0.0:9090` | admin HTTP listen |
| `FLX_ADMIN_API_KEY` | (off) | API key (`X-API-Key`) |
| `FLX_ADMIN_TOKEN` | (off) | Bearer token |
| `FLX_ADMIN_BASIC_USER` / `..._PASS` | (off) | HTTP Basic auth |
| `FLX_DISCORD_WEBHOOK_URL` | (off) | quota alert webhook |
| `FLX_WORKER_COUNT` | `10` | Workers per account |
| `FLX_WORKER_PREFIX` | `flarex-` | Worker script name prefix |
| `FLX_WORKER_BACKEND` | `workers_dev` | `auto` / `workers_dev` / `custom_domain` |
| `FLX_LOG_LEVEL` | `info` | `trace` / `debug` / `info` / `warn` / `error` |
| `FLX_STATE_PATH` | `/state/flarex.db` | bbolt state path |

### Quick start

```bash
cat > .env <<EOF
FLX_HMAC_SECRET=$(openssl rand -hex 32)
FLX_TOKENS=cf_api_token_with_workers_edit
FLX_ADMIN_API_KEY=$(openssl rand -hex 16)
FLX_DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/.../...
EOF

docker-compose -f deploy/docker-compose.yml up -d
docker-compose -f deploy/docker-compose.yml logs -f
```

In Portainer: paste `docker-compose.yml` into a new Stack and fill the
"Environment variables" section with the same keys.

State (bbolt DB) is persisted in the named volume declared at the bottom of
`docker-compose.yml` (currently `flarex-state` — a legacy name kept for
compatibility with existing deployments). Workers remain deployed across
restarts.

## systemd

```bash
sudo install -m 0755 ./bin/flarex /usr/local/bin/
sudo install -d /etc/flarex /var/lib/flarex
sudo install -m 0600 deploy/config.yaml /etc/flarex/
sudo useradd --system --home /var/lib/flarex --shell /usr/sbin/nologin flarex
sudo install -m 0644 deploy/flarex.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now flarex
journalctl -u flarex -f
```

Secrets such as `FLX_HMAC_SECRET` go in `/etc/flarex/env`
(referenced by the unit file, mode `0600`):

```
FLX_HMAC_SECRET=<32+ char random>
FLX_TOKENS=<token>
```
