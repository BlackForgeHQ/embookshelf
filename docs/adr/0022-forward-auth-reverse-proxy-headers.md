# ADR-0022: Forward-auth via trusted reverse-proxy headers

- Status: Accepted (2026-05-09)
- Deciders: Bohdan Shaparenko (@shbodya)

## Context

Self-hosted users front embookshelf with an SSO-aware reverse proxy
(Authelia, oauth2-proxy, Traefik forwardAuth, Cloudflare Access).
Those proxies authenticate the user themselves and inject identity
headers — typically `Remote-User`, `Remote-Email`, `Remote-Name`,
`Remote-Groups` — on every proxied request.

The existing OIDC login flow (ADR-0007 + the three OIDC provider
slugs) does not cover this shape. Two reasons it can't:

1. Authelia in forward-auth mode never speaks OIDC to the upstream
   app — it terminates the protocol at the proxy and degrades the
   contract to "trust these headers." Running OIDC on top would mean
   double-authenticating the user.
2. Some deployments (Cloudflare Access, Tailscale serve) do not
   expose an OIDC issuer for the upstream app to integrate against
   in the first place. Headers are the only signal.

Without first-class support, operators today either give up on SSO,
hand-roll header-stripping middleware in front of embookshelf, or
fall back to local passwords + a separate proxy auth. We want a
direct integration that does not regress the OIDC path or the OPDS
HTTP Basic path.

## Decision

Add a `Forward-auth` middleware that materializes proxy-injected
identity headers as a `user_identities` row and attaches the matching
`users` row to the request context. Configuration lives in
`app_settings.FORWARD_AUTH` (JSON, both dialects, no schema
migration — the row is K/V):

```json
{
  "enabled": true,
  "trustedProxyCIDRs": ["10.0.0.0/8", "127.0.0.1/32"],
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

### Trust gate — IP allowlist only

The middleware reads identity headers iff the immediate TCP peer
(`c.Request.RemoteAddr`) falls inside `trustedProxyCIDRs`.

`X-Forwarded-For`, `X-Real-IP`, and any other client-controllable
hop hint are ignored. Trusting them would let any caller forge a
trusted source address and impersonate any user.

The deployment contract is therefore: **the forward-auth proxy must
be the immediate upstream of embookshelf** (typically same Docker
network, or `127.0.0.1` for systemd-style colocated deployments).
Sandwiching another L4 LB between proxy and embookshelf is
unsupported.

Boot validation refuses to start when `enabled=true` and
`trustedProxyCIDRs` is empty — mirrors the Cipher bad-key behavior
from ADR-0010. Misconfiguration that would silently disable the gate
and accept any source address must be fatal, not warning-level.

### Stateless per-request, no session cookie

The middleware does not mint an `embookshelf_session` row. It looks
up `(provider='proxy', subject=<Remote-User>)` in `user_identities`
on every request and attaches the resolved user to context. A
30-second in-process LRU keyed on `(slug, subject)` absorbs the hot
path; cache invalidation on user role/email change is best-effort
(cache TTL bounds staleness).

This contract matches what proxy-trust actually means: the proxy is
the source of truth for the user's auth state. A session cookie
would create a window where the user is logged out at Authelia but
still has a live cookie inside embookshelf — exactly the failure
mode operators adopt forward-auth to avoid.

CSRF: forward-auth requests skip `CSRFGuard`. The trusted-IP gate
already establishes that the request originated at the proxy; the
proxy itself enforces same-origin for its own session cookies. Adding
an Origin/Referer check on top blocks legitimate proxy-to-app calls
without strengthening the trust story.

### Reuse `user_identities` with slug `proxy`

A new fourth slug `proxy` joins `google`, `github`, `generic`. This
generalizes the term: `user_identities` becomes the home for any
**External identity provider** (CONTEXT.md). Lockout guard, Linking,
Auto-link, and Provisioning all apply unchanged. ADR-0007 is not
superseded — the schema fits the new slug as-is.

`subject` = the value of the configured user header (default
`Remote-User`). This is the identity key. `email` = the value of the
email header, used as the auto-link helper exactly the way OIDC
auto-link uses the verified-email claim. Trust in the email is
trust in the proxy; this is the contract documented for operators.

### Provisioning shares OIDC's policy row

`oidc_auto_provision_details` (`EnableAutoProvisioning`,
`RequireAdminApproval`, `DefaultRole`) governs forward-auth too. One
admin toggle, both auth paths. The table name is historical; the
semantics are now external-identity-wide.

The empty-instance carve-out from CONTEXT.md → "Provisioning"
applies: the first forward-auth hit on an empty `users` table is
admitted as admin, so the operator who has already gated the app at
the proxy can bootstrap without enabling local passwords first.

Group→role mapping (`Remote-Groups` → user/admin) is **out of scope**
for this ADR. It is a follow-up that will need its own decision on
mapping shape (allowlist? regex? config DSL?).

### Auth path matrix

Forward-auth applies only to browser/SPA routes currently behind
`RequireAuth`. The middleware composes as an alternative to session
auth: if the request matches the trusted CIDR and carries the user
header, the forward-auth path resolves the user; otherwise the chain
falls through to session-cookie auth as before. OPDS endpoints stay
on `BasicAuth` — e-readers cannot traverse Authelia's redirect dance
and need a stable Basic challenge.

### Account panel

The proxy identity renders read-only:
"Reverse proxy: <Remote-Email> — managed by your administrator." No
Connect / Disconnect buttons. Disconnecting would delete the row,
which the next request would recreate via auto-link or
auto-provision; the affordance would only mislead.

The row still counts toward Lockout guard. A user whose only
credential is a proxy identity is blocked from removing their last
OIDC link or password until they have set another credential —
identical to the existing rule, applied to the new slug.

### Login page + logout

When `enabled && hideLocalLogin`, the `/login` page renders a notice
("This deployment uses upstream SSO via your reverse proxy") and
hides the local-password form. SPA's 401 handler redirects to
`logoutUrl` when set, otherwise shows the notice.

`POST /api/v1/auth/logout` returns `{logoutUrl}` so the SPA can
redirect to the proxy's logout endpoint (e.g. Authelia's `/logout`).
The endpoint is a no-op for forward-auth users — there is no cookie
to clear.

## Consequences

Positive:

- One config change (`FORWARD_AUTH` row) integrates Authelia,
  oauth2-proxy, Traefik forwardAuth, or Cloudflare Access without
  touching code.
- Stateless per-request match avoids the "logged out at SSO, still
  in app" footgun that an issued session cookie would create.
- Lockout guard, Auto-link, and Provisioning are exercised by both
  auth paths — fewer code paths to keep correct.

Negative:

- Trust hinges on a correctly-set `trustedProxyCIDRs`. A
  misconfiguration that lists the wrong CIDR and a proxy in front of
  it can silently downgrade safety — the boot refusal handles the
  empty-list case, but a wrongly-populated list is the operator's
  responsibility. Documentation must be loud about this.
- Per-request `users` lookup. The 30-second LRU bounds it, but on a
  cold cache every request takes a PK round-trip. Acceptable for the
  self-hosted deployment scale embookshelf targets; would need
  revisiting at higher RPS.
- "OIDC provider" terminology stretches: the slug is now stored in a
  table whose other slugs are pure OIDC. CONTEXT.md adds an
  "External identity provider" umbrella term to handle the cross-cut
  statements.

Neutral:

- No DB migration. `app_settings` already accepts new K/V rows,
  `user_identities.provider` already accepts arbitrary text.
- OPDS path untouched; Basic auth remains the e-reader contract.

## Alternatives considered

**Shared-secret header (`X-Forwarded-Auth-Token: <pre-shared>`)
instead of CIDR allowlist.** Rejected as default. CIDR matches the
overwhelmingly common deployment shape (proxy + app on the same
Docker network). Shared secret adds an unrotated string to leak; the
threat it covers — attacker on the same network — already implies
broader compromise than spoofing one header. Could be added later as
opt-in defense-in-depth without disturbing this design.

**Mint a session cookie on first forward-auth hit.** Rejected. The
proxy-logout-propagates-immediately property is the main reason to
adopt forward-auth in the first place; reintroducing a TTL'd cookie
breaks it.

**New `proxy_identities` table.** Rejected. Lockout guard and
auto-link logic would have to be duplicated. The `user_identities`
shape fits the new slug without schema work.

**Honor `X-Forwarded-For` to support deployments where the proxy is
not the immediate upstream.** Rejected outright — header-derived
source addresses cannot be the basis of an authentication trust
gate. Operators who need that topology run their proxies adjacent
to embookshelf.

**Auto-map `Remote-Groups` to user/admin role at login.** Deferred.
Group-to-role mapping is its own design problem (mapping DSL,
admin-revocation behavior, role-demotion side effects on shared
shelves per ADR-0017) that does not block forward-auth itself.
