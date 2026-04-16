[← Back to docs](README.md)

# Cloudflare Workers: limitations and why things don't work

A survival guide. Everything here is a direct consequence of how CF Workers
execute (V8 isolates at the edge, not VMs). These are **not** FlareX bugs;
they're hard constraints from the platform.

> Verified recipes: [4.4 SSH blocked by default port allowlist](recipes.md#44-ssh-over-the-socks5-proxy-port-22) · [4.6 CF-hosted target behaviour](recipes.md#46-socks5h-vs-socks5-dns-at-proxy-vs-at-client) · [8.4 RFC1918 SSRF defence](recipes.md#84-rfc1918-target-denied-before-the-dial).

## What a Worker can do

- Accept HTTP requests (`fetch(request)` handler).
- Open a raw TCP socket out with `cloudflare:sockets` `connect(host, port)`.
- Upgrade an incoming request to WebSocket (how FlareX tunnels traffic).
- Call `fetch()` to any URL (HTTP/HTTPS).
- 128 MB memory, 30 s wall time default (higher on paid).

## What a Worker cannot do

| Limitation | Impact on FlareX | Workaround |
|------------|------------------|------------|
| **No UDP** (no API for it) | UDP proxying is impossible. | Use a different tool (sing-box, shadowsocks-UDP-over-TCP). |
| **No raw IP sockets** | No ICMP ping / traceroute proxying. | N/A. |
| **`connect()` cannot reach Cloudflare IPs** | Socket mode 4001's on CF-hosted targets (many SaaS, anything behind CF). | **Fetch mode** or **hybrid** (auto-promote on byte-sniff). See [proxy-modes.md](proxy-modes.md). |
| **`fetch()` only does HTTP** | Can't wrap non-HTTP traffic. | For non-HTTP to CF-hosted targets: no workaround. Request will fail cleanly (not corrupt) in hybrid mode. |
| **`startTls()` requires HTTP handshake** | Prevented us from using it transparently. | FlareX does plain TCP and the client handles TLS end-to-end. |
| **WebSocket message size limit** | 1 MB per frame by default. | FlareX sets `SetReadLimit(-1)` and chunks streams via `io.CopyBuffer`. |
| **Outbound request budget** | CF subrequest cap (50 req/Worker on free, 1k on paid). | Recycle workers via `rotate_max_req` or scale count. |
| **Daily request quota** | Free: 100k req/day/account. Paid: 10M/mo included, billed beyond. | Multiple accounts (`tokens: [t1, t2, t3]`). Quota auto-pause at 100% ([configuration.md#quota](configuration.md#quota)). |
| **Script size** | 1 MB for a Worker script. | FlareX template is ~8 KB. Not a constraint unless you add heavy deps to `template_path`. |
| **No persistent filesystem** | Can't cache at the Worker. | FlareX caches at the proxy (DNS, CF-blocked hosts). |

## Why socket mode fails on CF-hosted targets

Cloudflare's `cloudflare:sockets` runtime **refuses** `connect()` to any IP
in the Cloudflare IP ranges. If it allowed it, you could loop a request
through CF → CF → CF → ... forever and multiply request volume cheaply. CF's
anti-abuse denies this at the runtime layer with close code `4001`.

Which IPs? The full list is at
[cloudflare.com/ips](https://www.cloudflare.com/ips/). Roughly
`173.245.48.0/20`, `103.21.244.0/22`, `104.16.0.0/13`, `172.64.0.0/13`,
`2400:cb00::/32`, `2606:4700::/32` etc. Everything "orange-clouded" sits
behind these IPs.

## How you tell "is the target on CF?"

Ways:

- `dig +short api.example.com` returns IP in a CF range.
- `curl -sI https://example.com | grep -i cf-ray` returns a CF ray header
  (proxied by CF).
- After a failed `socks5://` with a 4001 in the logs, FlareX cached the
  host:port in `cfUnreachable` for 10 min.

## Acceptable Use Policy (AUP)

Cloudflare's Workers [AUP](https://www.cloudflare.com/terms/) restricts using
Workers as a generic proxy. The relevant clauses forbid:

- Using the service primarily as a proxy to evade rate limits / IP bans.
- Scraping / crawling at scale.
- Circumventing IP-based access controls.

**FlareX is a dual-use tool.** Legit uses (testing your own infra, CTFs,
authorized pentests) are fine. Sustained scanning of third-party infra from
free-tier Workers is not, and CF will suspend accounts that do it.

Mitigations if you have a legit high-volume use case:

1. Use **paid Workers** (Workers Paid at $5/mo gets 10M requests, 30s CPU
   time, higher subrequest cap). Much more tolerant.
2. Rotate across **many accounts**, spread over different email addresses.
   FlareX supports `tokens[]` with N entries.
3. Respect target-side rate limits. `rate_limit.per_host_qps` is there for
   this.

## Regional behavior

Your Worker runs on **the CF PoP closest to the request origin** (your
FlareX process). Not random. IPs FlareX sends out of are geographically
correlated to where FlareX itself is.

If you run FlareX in Paris, your Workers will almost always egress from Paris
PoPs. Rotation happens *within* a PoP pool, not across continents.

To get global distribution: run FlareX instances in multiple regions.

## IPv4 vs IPv6 from the Worker

Worker egress is **dual-stack**. Targets like `ifconfig.me` with AAAA records
will often show an IPv6 Worker address. Force IPv4 with
`curl -4 -x socks5h://...` but the dest needs to have an A record too.
FlareX can't magic an A record where only AAAA exists.

## "My curl hangs / resets"

See [troubleshooting.md](troubleshooting.md). Most common cause: you used
`socks5://` (client-side DNS) and the IP your resolver gave happens to be a
CF IP. Socket fails, byte-sniff says "not HTTP" (client was sending
ClientHello), close. Use `socks5h://` so the Worker resolves.
