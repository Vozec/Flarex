[← Back to docs](README.md)

# Session stickiness

By default every client request lands on a random Worker (round-robin).
For stateful flows (cookie auth, anti-bot cookies, OAuth redirects, sharded
sessions on the target) you often want consecutive requests to come from the
**same egress IP**.

FlareX implements sticky sessions via the SOCKS5 / HTTP Basic username field.

> Verified end-to-end example + output: [recipes.md §3.7](recipes.md#37-sticky-session-via-socks5-username).

## How to ask for a sticky session

Append `-session-<id>` or `:session:<id>` to the configured `auth_user`:

```bash
# config.yaml
# listen: {auth_user: "alice", auth_pass: "secret"}

# Random session per curl run (rotation still every N requests within it)
curl -x socks5h://alice-session-$RANDOM:secret@127.0.0.1:1080 https://target
curl -x socks5h://alice-session-42:secret@127.0.0.1:1080 https://step1
curl -x socks5h://alice-session-42:secret@127.0.0.1:1080 https://step2
# both step1 + step2 go through the same Worker IP
```

Supported separators (case-sensitive):

- `user-session-<id>` (BrightData / Luminati convention)
- `user:session:<id>` (easier to paste in some clients)

If the username is exactly `auth_user`, no stickiness is applied (classic
round-robin).

## Without auth

If no auth is configured (`auth_user == ""`), session stickiness is **not
available** through this mechanism. Solutions:

1. Enable auth with a dummy password, use session suffix on the user.
2. Implement your own client-side pinning (e.g., keep a persistent SOCKS5
   connection per session; FlareX tunnels are 1:1 with client conns).

## How it works

```
client ── SOCKS5 CONNECT with user=alice-session-abc123 ──▶ flarex
                                                             │
                                                             ▼
                                                 extractSession() → "abc123"
                                                             │
                                                             ▼
                                                   FNV-1a hash(sessionID)
                                                             │
                                                             ▼
                                            hash % pool_size → worker index i
                                                             │
                                                             ▼
                                  workers[i] if Available() else round-robin fallback
```

Code: `internal/pool/pool.go:NextBySession()` +
`internal/proxy/socks5.go:extractSession()`.

## Semantics + edge cases

| Scenario | What happens |
|----------|--------------|
| Session ID you pick | Any opaque string. Short is fine. |
| Anchor Worker becomes unhealthy | Falls back to the next available Worker in the pool (round-robin sweep from hashed index). **IP changes.** |
| Anchor Worker recycled by rotation | Same: you'll land on a different Worker on the next dial. |
| Session ID that hashes to a recycled worker | New anchor is deterministic based on the hash, so you may get consistently routed to the new replacement. |
| Two different session IDs happen to hash to the same index | Normal (FNV mod pool size). They share a Worker. Sessions are *not* user identifiers, just routing hints. |
| Session ID empty | No stickiness (round-robin). |

**You cannot force stickiness longer than a Worker's lifetime.** If the
target genuinely pins sessions to IP (rare, usually they pin to cookies), a
Worker rotation will appear as a "new session" to the target.

## Why FNV instead of crypto hash

FNV-1a is fast (~2ns per call), and we just need uniform distribution, not
cryptographic pre-image resistance. A client can't exploit knowing the hash:
the pool changes over time and the Workers are behind our proxy, not
directly reachable.

## Use cases

- **Multi-step forms** with CSRF tokens bound to IP.
- **Anti-bot challenge** (Cloudflare Turnstile, Akamai Bot Manager) where a
  cookie is issued after passing a challenge and the cookie's scope is IP.
- **Scrapers** that hit an API where one endpoint returns a pagination
  cursor usable only from the same IP.

## Not a use case

- **Load balancing fairness**: that's the job of `strategy:
  least_inflight` (not yet implemented).
- **Audit / logging**: the session ID isn't logged by FlareX by default
  (trace level only) so you can't correlate client → session → Worker
  externally.
