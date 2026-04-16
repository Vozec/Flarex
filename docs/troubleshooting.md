[← Back to docs](README.md)

# Troubleshooting

Common errors + quickest fixes.

> Working commands + expected outputs in [recipes.md §9](recipes.md#9-edge-cases--failure-modes):
> [9.1 DB lock](recipes.md#91-two-servers-on-the-same-db-second-one-fails-fast),
> [9.4 invalid token](recipes.md#94-invalid-cf-token-fails-with-a-clear-error),
> [9.6 zero healthy workers](recipes.md#96-zero-healthy-workers--graceful-socks5-close),
> [9.7 quota 100%](recipes.md#97-quota-at-100-across-accounts--clean-close).

## Startup

### `error="open ./flarex.db: timeout" fatal`
bbolt lock is held by another FlareX process. Two instances, same DB file.

```bash
fuser flarex.db            # who holds the lock?
pkill -TERM flarex         # kill the other instance
rm -f flarex.db.lock 2>/dev/null   # if a stale lock file remains
```

Use separate `state.path` for each instance.

### `config freshly downloaded; edit it then re-run`
First-run bootstrap downloaded `config.example.yaml` into the path you
passed. Edit it to fill in real tokens + HMAC secret, then rerun.

Disable bootstrap: set `FLX_CONFIG_EXAMPLE_URL=` to an empty string, or
provide a real `config.yaml` before starting.

### `hmac_secret required (config or env FLX_HMAC_SECRET)`
Your config still has a placeholder or the env var is unset. Minimum
acceptable: 32 random chars.

```bash
openssl rand -hex 32   # good source
```

### `bad credentials` during `flarex deploy`
CF token is invalid or lacks scopes. Re-check:
- **Workers Scripts:Edit** at account level (minimum).
- **Zone.DNS:Edit** on target zones if you want `custom_domain` backend.

See [cloudflare-token.md](cloudflare-token.md) for the exact token setup.

## Runtime

### `curl: (97) cannot complete SOCKS5 connection`
curl couldn't agree a SOCKS5 method with the proxy. Common causes:

1. **Auth required but not provided.** `listen.auth_user` is set; pass
   `curl -x socks5h://USER:PASS@...`.
2. **Client speaks SOCKS4.** Use `-x socks5h://` or `--socks5-hostname`.
3. **Wrong port / not listening.** Check `ss -tlnp | grep 1080`.

### `Connection reset by peer` on HTTPS via `socks5://` (not `socks5h`)
Your client resolved the hostname locally to a **CF IP**. Socket dial
fails 4001, byte-sniff sees TLS ClientHello (`0x16 0x03 …`) → not HTTP →
safe close.

**Fix:** use `socks5h://` so the Worker resolves. The Worker sees all A +
AAAA records and picks non-CF when available.

### Requests hang for ~1 second then close
Your client sends **nothing** over the tunnel after CONNECT, so the
byte-sniff peek times out after 1s and closes. Rare in practice; every
well-behaved HTTPS/HTTP client sends at least a ClientHello or a request
line immediately.

If you're writing a custom client that expects the server to speak first
(e.g., SMTP, IRC, SSH), use hostnames that don't resolve to CF IPs; the
socket path will work without sniffing.

### `Invalid SSH identification string` on the remote server
Not an error from FlareX. Your client (e.g., `echo "" | nc`) sent empty
bytes to an SSH daemon; the daemon rejected them. Real SSH via
`ProxyCommand nc -X 5 -x …` works.

### `dial failed (all retries)`
Every Worker in the pool failed. Check `flarex client status`:

- `breaker: "open"` on every worker → CF account suspended or throttled.
- `healthy: false` everywhere → quota auto-paused (look for `quota_limit`
  alert), or probe is failing (`timeouts.probe` too tight, bump to 2s).

### `upstream blocked, falling back to fetch` (debug level)
Informational. Target is CF-hosted; hybrid fallback kicked in.

### `template drift, recycling` at startup
You edited `template.js`, rebuilt FlareX, but the live Workers still run
the old script. Not an error; FlareX auto-recycles them. Takes a few
seconds per Worker.

### 403 from target when everything else works
Target is blocking Cloudflare IP ranges. No workaround from the FlareX
side; you need a non-CF egress.

### Quota 100% alerts every day
Your `worker.count × len(accounts)` isn't enough for your traffic. Options:

1. Add more CF accounts → more tokens → more quota.
2. Upgrade one account to Paid ($5/mo = 10M requests/month).
3. Turn on `rate_limit.per_host_qps` to cap per-target burn.

### Logs: `breaker .* open-state`
Circuit breaker opened for that Worker. Recovers in `breaker_open`
seconds (default 30). Persistent opens = that Worker is bad, will be
recycled if rotation is on.

## Performance

### "It's slow"
Check, in order:

1. **`flarex client metrics` → `cft_dial_latency_seconds`.**
   - p50 < 50ms: fine, you're CPU-bound locally.
   - p50 > 200ms: CF-side latency, check CF status page.
2. **Concurrent tunnels saturating `worker.count`.** If you have 10 Workers
   and 200 concurrent requests, each Worker handles 20. Increase
   `worker.count`.
3. **File descriptor exhaustion.** `ulimit -n` → should be ≥ 8192. Systemd:
   `LimitNOFILE=65536`.

### High CPU on FlareX itself
`pprof` it:

```bash
go tool pprof -http :8080 \
  "http://user:pass@flarex:9090/debug/pprof/profile?seconds=30"
```

Require `admin.enable_pprof: true`.

## Development

### `go test` fails with `cfip` import
You've got a stale build cache referencing the now-deleted `internal/cfip`
package:

```bash
go clean -cache
go mod tidy
go test ./...
```

### `make: Rien à faire pour « build »`
The binary file is newer than all sources. Force rebuild:

```bash
make rebuild
```

or bump VERSION manually: `make rebuild VERSION=0.1.0-alpha`.

### New config field isn't picked up
Some fields hot-reload via the admin UI's editable Config page (or
`PATCH /config`); the rest need a restart. The UI flags every field
either ✅ live or ⚠ restart. See
[admin-web-ui.md#config--editable](admin-web-ui.md#config--editable).

## Web UI

### Browser pops a Basic-auth dialog at `/ui/`
Old behaviour from before the cookie-session rewrite: the 401 advertised
`WWW-Authenticate: Basic` even when `basic_user` wasn't configured, and
some browsers cache the rejected creds. Fix:

1. Update to a build that emits `WWW-Authenticate: Bearer` when no Basic
   user is set (current default).
2. Clear cached HTTP creds for the origin (Chrome: DevTools → Network →
   "Disable cache" → reload; or open in incognito).
3. Make sure `admin.api_key` (not `basic_user`/`basic_pass`) is what
   you're using; the SPA only knows about the api_key login form.

### `{"error":"dial: upstream blocked (likely CF-hosted target)"}` on /test-request
The target is CF-hosted and you're hitting it over HTTPS. The byte-sniff
fallback only promotes confirmed HTTP traffic (it can't wrap the client's
TLS ClientHello inside the Worker's `fetch()` call). The /test-request
handler itself **does** auto-promote to fetch on `ErrUpstreamBlocked`
since it generates the request server-side; make sure you're on a build
≥ the test-request fetch-fallback fix. For HTTPS clients via SOCKS5
flow, use a non-CF target or set `pool.proxy_mode: fetch` and accept that
TLS terminates at the CF edge.

### TOTP code rejected
Your server clock and the device that generated the code must be within
~30 s of each other. NTP fixes 99% of cases. Test with `oathtool --totp
-b $FLX_ADMIN_TOTP_SECRET` from the same host that runs `flarex server`.
If that code works in the UI, your secret is correct.

### `409 account already registered` on POST /tokens
You're re-posting a token whose account already has live workers in the
pool. Use the **Accounts** tab → click your account → **Add more
workers** instead (uses the stored token, no re-paste).

## Still stuck?

1. Enable `log.level: debug`; often self-explanatory.
2. `flarex client status` for a worker-level view.
3. Open a Worker log tail:
   ```bash
   curl -N -H "X-API-Key: $KEY" \
        http://flarex:9090/workers/<name>/logs
   ```
   Real-time `console.log` from inside the Worker runtime.
4. File an issue with `flarex --version` + a minimal repro.
