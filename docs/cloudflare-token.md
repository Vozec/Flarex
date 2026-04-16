[← Back to README](../README.md)

# Creating a Cloudflare API Token for FlareX

> **When to read this**: you are setting up FlareX for the first time and need
> a Cloudflare API token with the right scopes, or you are rotating an existing
> token.

FlareX talks to the Cloudflare API to upload, list, and delete
Worker scripts (and optionally bind them to your own domains).

> A free Cloudflare account is enough.
> The token needs **account-level** access (not just a zone). The default
> "Global API Key" works but is overpowered; prefer a scoped token.

## Quick recipe (workers.dev only, recommended)

If you just want to use `*.workers.dev` URLs (no custom domain on your zone),
this is all you need.

1. Go to <https://dash.cloudflare.com/profile/api-tokens>.
2. Click **Create Token** → **Create Custom Token**.
3. Name it: `FlareX`.
4. Add **one** permission row:
   - **Resource type**: `Account`
   - **Permission group**: `Workers Scripts`
   - **Level**: `Edit`
5. Under **Account Resources**:
   - **Include**: pick the account you want FlareX to deploy on
     (or **All accounts** if the token may manage several).
6. Optional:
   - **Client IP Address Filtering**: lock the token to your IP / CIDR.
   - **TTL**: set an end date (rotate quarterly).
7. Click **Continue to summary** → **Create Token**.
8. Copy the token (starts with `cfut_…` or similar). You will not see it again.

That's it. Drop the token in `config.yaml`:

```yaml
tokens:
  - "cfut_paste_your_token_here"
```

FlareX will auto-discover the account ID and the `*.workers.dev`
subdomain on first run (creating the subdomain if absent).

## Recipe with custom domains on your zone

If you want Workers reachable as `flarex-XXX.your-domain.tld` instead of
`*.workers.dev`, give the token DNS edit access on the zones you own.

Add a **second** permission row:
- **Resource type**: `Zone`
- **Permission group**: `DNS`
- **Level**: `Edit`

Under **Zone Resources**:
- **Include**: pick the zone(s) (e.g. `bughunter.tld`), or **All zones** if you
  want FlareX to spread Workers across all your zones.

Final token has 2 rows:

| Resource | Permission | Level |
|----------|------------|-------|
| Account | Workers Scripts | Edit |
| Zone | DNS | Edit |

FlareX will then auto-pick the `custom_domain` backend (override via
`worker.deploy_backend: workers_dev | custom_domain | auto` in config).

## Verifying the token

```bash
TOKEN="cfut_xxx"
curl -s -H "Authorization: Bearer $TOKEN" \
  https://api.cloudflare.com/client/v4/user/tokens/verify \
  | jq '.success, .result.status'
# expect: true, "active"

curl -s -H "Authorization: Bearer $TOKEN" \
  https://api.cloudflare.com/client/v4/accounts \
  | jq '.result[] | {id, name}'
# expect: at least one account ID
```

If the second call returns `[]`, the token is missing account access. Re-edit
it and add the **Account** resource.

## What FlareX does with the token

Endpoints called (verb / path):

| Action | Method | Path |
|--------|--------|------|
| List accounts | `GET` | `/accounts` |
| Read workers.dev subdomain | `GET` | `/accounts/{id}/workers/subdomain` |
| Create workers.dev subdomain | `PUT` | `/accounts/{id}/workers/subdomain` |
| List Workers | `GET` | `/accounts/{id}/workers/scripts` |
| Upload Worker script | `PUT` | `/accounts/{id}/workers/scripts/{name}` |
| Enable Worker on workers.dev | `POST` | `/accounts/{id}/workers/scripts/{name}/subdomain` |
| Delete Worker script | `DELETE` | `/accounts/{id}/workers/scripts/{name}` |
| **Custom domain only:** | | |
| List zones | `GET` | `/zones` |
| List DNS records | `GET` | `/zones/{id}/dns_records` |
| Bind Worker to hostname | `PUT` | `/accounts/{id}/workers/domains` |
| Unbind Worker from hostname | `DELETE` | `/accounts/{id}/workers/domains/{id}` |
| Delete DNS record | `DELETE` | `/zones/{id}/dns_records/{id}` |

The token never leaves your machine; it is sent only to `api.cloudflare.com`
over TLS.

## Multi-account setup

You can pass several tokens. Each one is independently discovered.

```yaml
tokens:
  - "cfut_token_account_A"
  - "cfut_token_account_B"
```

FlareX deduplicates discovered accounts (key = `accountID + token`)
and load-balances across the union of their Workers.

> Cloudflare's AUP discourages running multiple accounts purely to multiply
> free quotas. Use at your own risk and prefer **Workers Paid** ($5/mo, 10M
> req/mo included) for sustained workloads.

## Rotating / revoking

- Edit or revoke at any time from the same dashboard page. The change is
  effective immediately on Cloudflare's side.
- After revoking, run `flarex destroy` BEFORE killing the token if
  you want to clean Workers from CF. Otherwise, leftover Workers stay until
  you delete them by hand.

## Troubleshooting

**"Authentication error" on `/accounts`**
- Token lacks the Account resource. Edit and add at least one account.

**"You do not have a workers.dev subdomain"**
- The Workers feature was never used on this account. Either:
  - Open dashboard → Workers & Pages once (creates a subdomain), OR
  - Let FlareX create one with `flarex server -c ...`
    (it issues `PUT /accounts/{id}/workers/subdomain` with a generated name).

**"This API Token is invalid"**
- Wrong token, expired, or hit IP filter. Verify with the curl snippet above.

**"Workers Subdomain" permission missing in the dashboard**
- That row was renamed/merged. `Workers Scripts: Edit` covers per-script
  subdomain enablement; the only thing it cannot do is rename the
  account-level subdomain (which FlareX does not do).
