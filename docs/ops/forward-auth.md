# Forward auth (reverse-proxy header SSO)

Use forward auth when an upstream proxy already terminates SSO and
forwards identity headers on every request. Complements OIDC — pick
one path per browser, not both. ADR-0022.

Supported proxies (anything emitting identity headers works):

- **Authelia** (`Remote-User` / `Remote-Email` / `Remote-Name` / `Remote-Groups`) — default header set.
- **oauth2-proxy** (`X-Forwarded-User` / `X-Forwarded-Email` / `X-Forwarded-Preferred-Username` / `X-Forwarded-Groups`).
- **Traefik forwardAuth** with any upstream emitting Authelia-style or oauth2-proxy-style headers.
- **Cloudflare Access** (`Cf-Access-Authenticated-User-Email`).
- Anything else that injects a stable user header on proxied requests.

## Trust model

embookshelf trusts the headers **only when the request's immediate TCP
peer matches `trustedProxyCIDRs`**. `X-Forwarded-For` and `X-Real-IP`
are deliberately ignored — honoring them would let any caller spoof
the source address and forge identity.

Deployment shape required:

```
[browser] → [forward-auth proxy] → [embookshelf]
```

The proxy must be the immediate upstream. Sandwiching another L4 LB
between proxy and embookshelf is unsupported. Boot refuses to start
when forward-auth is enabled with an empty `trustedProxyCIDRs`.

## Quick start (Authelia + Docker network)

`compose.yml` snippet:

```yaml
services:
  embookshelf:
    image: ghcr.io/blackforge/embookshelf:latest
    networks: [internal]
    # No published ports — only Authelia reaches embookshelf.

  authelia:
    image: authelia/authelia:latest
    networks: [internal]

  caddy:
    image: caddy:latest
    networks: [internal]
    ports: ["443:443"]
    # forward_auth → authelia, then proxy → embookshelf

networks:
  internal:
```

Caddyfile:

```caddyfile
books.example.com {
    forward_auth authelia:9091 {
        uri /api/verify?rd=https://auth.example.com
        copy_headers Remote-User Remote-Email Remote-Name Remote-Groups
    }
    reverse_proxy embookshelf:6060
}
```

Inside embookshelf, `Settings → Auth → Forward auth`:

```json
{
  "enabled": true,
  "trustedProxyCIDRs": ["172.16.0.0/12"],
  "headers": {
    "user":   "Remote-User",
    "email":  "Remote-Email",
    "name":   "Remote-Name",
    "groups": "Remote-Groups"
  },
  "logoutUrl": "https://auth.example.com/logout",
  "hideLocalLogin": true
}
```

`172.16.0.0/12` is the default Docker bridge range — list whatever
network the proxy actually opens its TCP connection from. `docker
inspect` the proxy container and copy the `IPAddress` if you want a
single-host CIDR.

## Field reference

| Field | Purpose |
|-|-|
| `enabled` | Master switch. When false the middleware is a no-op. |
| `trustedProxyCIDRs` | Required when `enabled=true`. List the IP/range of the proxy as seen by embookshelf's listening socket — i.e. the immediate TCP peer. Multiple entries OK. |
| `headers.user` | Required. Stable per-user identifier; becomes the `subject` on the `user_identities` row. Default `Remote-User`. |
| `headers.email` | Auto-link helper — when an existing user matches this email, the proxy identity attaches to them instead of creating a new user (gated by `AllowLocalAccountLinking`). Default `Remote-Email`. |
| `headers.name` | Display name for new users. Default `Remote-Name`. |
| `headers.groups` | Reserved for future group→role mapping. Read but not yet acted on. Default `Remote-Groups`. |
| `logoutUrl` | Optional. Returned by `POST /api/v1/auth/logout` so the SPA can redirect to the proxy's logout endpoint (e.g. Authelia `/logout`). |
| `hideLocalLogin` | Hides the local-password form on `/login` and shows an "SSO via your reverse proxy" notice. Local form still reachable at `/login?local=true` for break-glass admin access. |

## Provisioning

Forward-auth shares the OIDC auto-provision row
(`Settings → OIDC / SSO → Auto-provisioning`):

- `EnableAutoProvisioning` — when on, an unknown proxy identity creates a new user. When off, only existing users (matched by email) can sign in via forward-auth.
- `AllowLocalAccountLinking` — when on, the proxy identity attaches to a local-password user with the same email on first sign-in.
- `RequireAdminApproval` — new users land in `pending` status and cannot use the app until an admin approves them in `Settings → Users`.
- `DefaultRole` — `user` or `admin`. Auto-provisioned users get this role.

**First-user carve-out**: the very first proxy hit on an empty users
table is admitted as admin regardless of provisioning settings.
Otherwise an admin-less install with auto-provisioning off is
unrecoverable.

## What forward auth does NOT do

- **Mint a session cookie.** The proxy is the source of truth; every
  request re-checks the headers. Logging out at the proxy
  immediately logs you out of embookshelf — no TTL drift.
- **Cover OPDS endpoints.** E-readers (KOReader, Moon+ Reader, …)
  speak HTTP Basic and can't traverse a redirect-driven SSO flow.
  `/opds/**` stays on Basic auth even when forward-auth is enabled.
- **Map groups to roles.** `Remote-Groups` is read into the resolver
  for future use; today role assignment is via `DefaultRole` +
  manual change. Group-to-role mapping is on the roadmap.
- **Honor `X-Forwarded-For` for the trust gate.** Source IP comes
  from the immediate TCP peer only. If your topology hides the proxy
  behind another L4 hop, redesign so the proxy is adjacent to
  embookshelf — there is no header that lets you express "this came
  from 10.0.0.5 even though the connection is from 1.2.3.4" safely.

## Break-glass admin access

Local password login still works alongside forward auth:

1. Set `hideLocalLogin: false` in the forward-auth config (or visit
   `/login?local=true` directly).
2. Whitelist `/login` and `/api/v1/auth/login` at the proxy so they
   bypass SSO.
3. Sign in with email + password. Local sessions and forward-auth
   sessions coexist without conflict — the user is the same row.

If forward-auth was misconfigured and admins are locked out, edit
`app_settings` directly:

```sql
UPDATE app_settings
SET value = jsonb_set(value, '{enabled}', 'false')
WHERE name = 'FORWARD_AUTH';
```

(SQLite: same query, `json_set` instead of `jsonb_set`.) Restart the
process; the local login form returns.

## Observability

- `forward_auth enabled` log line at boot lists the trusted CIDRs and
  configured user header.
- Failed identity resolutions land as 401s in the access log, same as
  any other unauthenticated hit. The middleware never logs header
  values to avoid leaking PII.
- Account panel shows the proxy identity as a read-only row labelled
  "Reverse proxy: <Remote-Email> — managed by your administrator."

## See also

- ADR-0022 — design rationale and rejected alternatives.
- CONTEXT.md → "Forward-auth", "Trusted proxy CIDR", "Proxy identity".
- ADR-0007 — `user_identities` table that backs both OIDC and proxy slugs.
