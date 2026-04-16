[← Back to README](../../README.md)

# End-to-end test stack

> **When to read this**: you want to run the E2E benchmark / integration suite
> locally or in CI, without provisioning real Cloudflare Workers. For a real
> deployment, see [`deploy/`](../../deploy/README.md).

Local-only stack used by CI and developers to validate the SOCKS5 → Worker →
target chain **without touching real Cloudflare**.

| Service | Role |
|---------|------|
| `target` | nginx — fake target that the proxy hits |
| `mockworker` | Go binary in `cmd/mockworker` that mimics a CF Worker (WS upgrade + HMAC verify + raw TCP `connect()` + optional `startTls()`) |
| `seed` | one-shot: pre-populates the bbolt state with one Worker pointing at `mockworker` (skips the CF API entirely) |
| `flarex` | the proxy itself |
| `bench` | runs 5 curl requests through SOCKS5 to validate everything works |

```bash
docker-compose -f test/deploy/docker-compose.yml up --build --exit-code-from bench
```

This is **not** for production. For real deployment see
[`deploy/`](../../deploy/README.md).
