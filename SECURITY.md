# Security policy

## Reporting a vulnerability

Please do **not** file public GitHub issues for security problems. Instead:

1. Email **security@vozec.dev** (or open a
   [private security advisory](https://github.com/Vozec/flarex/security/advisories/new)
   on GitHub).
2. Include a minimal reproducer if possible, and the affected version
   (`flarex version`).
3. Expect an acknowledgement within 3 days.

We aim to publish a fix + advisory within 14 days for confirmed issues.

## Supported versions

Only the latest release is patched. FlareX is pre-1.0, so we reserve the
right to break APIs between minor versions — pin to a specific
`github.com/Vozec/flarex@vX.Y.Z` tag in your own build.

## Threat model

FlareX is a **proxy**. It sits between a trusted client and an untrusted
destination. Key assumptions:

- **The proxy operator owns the machine.** Local attackers with shell
  access can read the bbolt state file (`flarex.db`) and config.yaml —
  including the HMAC secret. Treat the host as trusted.
- **The Cloudflare API token is secret.** If leaked, an attacker can
  deploy/delete Workers on that account. Rotate via the CF dashboard.
- **The admin HTTP is authenticated.** If `admin.addr` is not loopback
  AND no auth is set, that's a misconfiguration — the server logs a
  warning but still binds. Do not expose the admin port to the public
  internet without auth.
- **The proxy is not anonymizing**. Egress IPs are Cloudflare's,
  attributable to the CF account. Consider this when running against
  targets that log IP + timestamp.
- **The Worker runtime is Cloudflare's.** Any CF runtime vulnerability
  (V8 escape, `cloudflare:sockets` bypass) is out of scope — report to
  Cloudflare.

## What's in scope

Bugs in the FlareX Go code or embedded Worker template that:
- Allow an unauthenticated client to bypass SSRF filters / port
  allowlist.
- Leak the HMAC secret via a response, log line, or error.
- Let a target server read a FlareX admin credential from an error path.
- Panic or deadlock the server from a malformed SOCKS5 greeting or HTTP
  request line.
- Permit HTTP smuggling between the HTTP CONNECT frontend and the
  upstream Worker.

## What's NOT in scope

- **Abuse of the Workers runtime** (using FlareX to evade a target's
  rate limit / WAF, or running banned workloads). That's a Cloudflare
  AUP concern, not a FlareX security bug.
- **Resource exhaustion via valid requests** (one client making a
  million SOCKS5 handshakes). Use `pool.goroutine_size`,
  `rate_limit.per_host_qps`, and OS-level ulimits as designed.
- **Side channels on the local machine** (a co-tenant reading memory).
  Use OS isolation.
