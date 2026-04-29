# OIDC Settings — Feature Specification

> Configure OpenID Connect (OIDC) for single sign-on with external identity providers like Authentik, Authelia, or Pocket ID.

- **Status:** Shipped
- **Scope:** `booklore-api` (Go) + `booklore-ui` (Angular)
- **Permission required:** `ADMIN` (settings read/write, group mappings, connection test)
- **Settings location:** Settings → Authentication → OIDC

---

## 1. Purpose

OIDC SSO lets an administrator delegate authentication to an external identity provider. BookLore becomes an OIDC Relying Party using the Authorization Code flow with PKCE (S256). On successful login the backend provisions a BookLore user (or links to an existing one), issues its own JWT access/refresh tokens, and optionally maps OIDC group claims to BookLore permissions and library access. Back-channel logout keeps BookLore sessions in sync when the provider ends an SSO session.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Admin | Configure an OIDC provider (issuer, client id/secret, scopes, claim mapping) | Users can log in with our existing SSO |
| 2 | Admin | Test the OIDC connection before enabling it | I catch misconfiguration (bad issuer, missing scopes, no PKCE) up front |
| 3 | Admin | Auto-provision users on first login with default permissions and libraries | I don't have to pre-create every account |
| 4 | Admin | Link an OIDC identity to an existing local account by username | Existing users keep their history when we turn on SSO |
| 5 | Admin | Map OIDC group claims to BookLore permissions and libraries | Access follows directory group membership |
| 6 | Admin | Enforce OIDC-only login (hide local password form) | I can retire local passwords once SSO is proven |
| 7 | Admin | Configure session duration for OIDC-issued tokens | Match our corporate session policy |
| 8 | User | Click "Sign in with {Provider}" and be redirected back logged in | No second password to remember |
| 9 | Security | End BookLore sessions when the provider sends a back-channel logout | Revoked SSO sessions don't leave BookLore open |

---

## 3. Configuration Model

### 3.1 Settings hierarchy

All OIDC state lives in the shared `app_settings` table keyed by `AppSettingKey` string constants. Nothing is copied from environment or config files at startup — changes take effect immediately.

| Key | Type | Public? | Purpose |
|-----|------|---------|---------|
| `OIDC_ENABLED` | bool | **Yes** | Master enable switch |
| `OIDC_PROVIDER_DETAILS` | JSONB (`OidcProviderDetails`) | **Yes** (ClientSecret stripped) | Issuer, client credentials, scopes, claim mapping |
| `OIDC_AUTO_PROVISION_DETAILS` | JSONB (`OidcAutoProvisionDetails`) | No | Auto-provision flag, linking flag, default perms/libraries |
| `OIDC_SESSION_DURATION_HOURS` | `*int`, nullable | No | Overrides default BookLore JWT lifetime for OIDC logins |
| `OIDC_GROUP_SYNC_MODE` | string: `DISABLED` \| `ON_LOGIN` \| `ON_LOGIN_ADDITIVE` | No | How group claims map to perms/libraries each login |
| `OIDC_FORCE_ONLY_MODE` | bool | **Yes** | Hide local login form; auto-redirect to provider |

The "public" settings are served unauthenticated via `/api/v1/public-settings` so the login page can render the "Sign in with …" button without leaking secrets.

### 3.2 Data flow summary

```
 Login page → OidcService.buildAuthUrl() → provider
                                            │
                provider → /oauth2-callback (Angular)
                                            │
     OidcCallbackComponent → POST /api/v1/auth/oidc/callback
                                            │
   OidcAuthService.ExchangeCodeForTokens() ─┤
     ├─ OidcTokenClient.ExchangeAuthorizationCode()
     ├─ OidcTokenValidator.ValidateIDToken()
     ├─ OidcTokenClient.FetchUserInfo()
     ├─ OidcClaimExtractor.ExtractClaims()
     ├─ FindOrProvisionUser() / LinkExistingUser()
     ├─ OidcGroupMappingService.SyncUserGroups()
     ├─ persist OidcSession
     └─ issue BookLore JWT → return to UI
```

---

## 4. Form Fields (Authentication Settings UI)

Reference: [authentication-settings.component.ts](booklore-ui/src/app/core/security/oauth2-management/authentication-settings.component.ts), [authentication-settings.component.html](booklore-ui/src/app/core/security/oauth2-management/authentication-settings.component.html).

### 4.1 Provider configuration

| Field | Type | Required | Default | Notes |
|-------|------|----------|---------|-------|
| `providerName` | string | **Yes** | `""` | Shown on the login page ("Sign in with Authentik"). |
| `clientId` | string | **Yes** | `""` | OAuth2 client id. |
| `clientSecret` | string (password input) | Conditional | `""` | Required for confidential clients; may be empty for public PKCE-only clients. Never returned via `/public-settings`. |
| `issuerUri` | URL | **Yes** | `""` | Base issuer URL; `.well-known/openid-configuration` is resolved from this. |
| `scopes` | string (space-separated) | No | `openid profile email groups offline_access` | `openid` is always required; `offline_access` enables refresh tokens. |
| `claimMapping.username` | string | **Yes** | `preferred_username` | Claim that produces the BookLore username. |
| `claimMapping.email` | string | **Yes** | `email` | |
| `claimMapping.name` | string | **Yes** | `given_name` | Used for display name. |
| `claimMapping.groups` | string | No | `groups` | Used for group → permission mapping. |

Enable toggle (`oidcEnabled`) is blocked until `providerName`, `clientId`, `issuerUri`, and all three required claim mappings are set (`isOidcFormComplete()` at [authentication-settings.component.ts:173](booklore-ui/src/app/core/security/oauth2-management/authentication-settings.component.ts:173)).

### 4.2 Auto provisioning

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `enableAutoProvisioning` | bool | `false` | If off, an unknown OIDC subject fails login unless a local account exists with a matching username and linking is allowed. |
| `allowLocalAccountLinking` | bool | `true` | Permits linking a LOCAL user → OIDC on first successful login when usernames match. |
| `defaultPermissions` | `[]string` | `[]` | e.g. `permissionRead`, `permissionDownload`. Applied to newly provisioned users. |
| `defaultLibraryIds` | `[]int64` | `[]` | Libraries granted to newly provisioned users. |

### 4.3 Session & group sync

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `oidcSessionDurationHours` | `*int` | `nil` | `nil` ⇒ use global JWT lifetime. |
| `oidcGroupSyncMode` | `DISABLED \| ON_LOGIN \| ON_LOGIN_ADDITIVE` | `DISABLED` | `ON_LOGIN` replaces perms/libs with mapping result on every login; `ON_LOGIN_ADDITIVE` unions them with what the user already has. |

### 4.4 OIDC-only mode

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `oidcForceOnlyMode` | bool | `false` | When true the login page hides the local password form and auto-redirects to the provider. Cannot be enabled unless OIDC is enabled and `issuerUri` + `clientId` are set (server-side guard in [AppSettingService.validateOidcForceOnlyMode()](booklore-api/internal/appsettings/service.go:90)). |

### 4.5 Read-only info panel

The settings screen shows values the admin needs to register with their provider:

- Redirect URI — `${origin}/oauth2-callback`
- Post-Logout Redirect URI — `${origin}/login`
- Back-Channel Logout URI — `${origin}/api/v1/auth/oidc/backchannel-logout`
- Required Scopes — `openid profile email groups offline_access`
- PKCE Method — `S256`
- Grant Type — `Authorization Code`

### 4.6 Group mappings (separate table)

Each mapping assigns BookLore permissions/libraries to users whose `groups` claim contains a given value. Managed via a dialog in the same settings page ([OidcGroupMappingHandler](booklore-api/internal/oidc/group_mapping_handler.go)).

| Field | Type | Notes |
|-------|------|-------|
| `oidcGroupClaim` | string (unique) | Exact group name from the provider. |
| `isAdmin` | bool | Grants admin role when the user belongs to this group. |
| `permissions` | `[]string` | Permission strings applied. |
| `libraryIds` | `[]int64` | Libraries granted. |
| `description` | string | Free-form admin note. |

---

## 5. API Surface

### 5.1 Unauthenticated OIDC flow

```
GET  /api/v1/auth/oidc/state
  → { state: string }                        -- 32-byte random, base64url; 5-min TTL, single-use

POST /api/v1/auth/oidc/callback
  Body:     OidcCallbackRequest { code, codeVerifier, redirectUri, nonce, state }   (all validate:"required")
  Response: { accessToken, refreshToken, isDefaultPassword }

GET  /api/v1/auth/oidc/redirect?code&codeVerifier&redirect_uri&nonce&state&app_redirect_uri
  → HTTP 302 Location: <app_redirect_uri>#access_token=...                          -- mobile deep link

POST /api/v1/auth/oidc/mobile/callback
  Body:     form-encoded (code, codeVerifier, redirect_uri, nonce, state)
  Response: { accessToken, refreshToken, isDefaultPassword }

POST /api/v1/auth/oidc/backchannel-logout
  Body:     form-encoded logout_token=<JWT>                                         -- OIDC Back-Channel Logout 1.0
  → 200 OK  (even for unknown sessions, by design)
```

Handler: [oidc_auth_handler.go](booklore-api/internal/oidc/oidc_auth_handler.go).
Open-redirect guard: `validateRedirectUri` at [oidc_auth_service.go:119](booklore-api/internal/oidc/oidc_auth_service.go:119) — accepts `booklore://oauth2-callback` or an `https?://` URL whose path ends with `/oauth2-callback` and whose origin matches the request.

### 5.2 Admin settings

```
GET    /api/v1/settings                                   -- full AppSettings (requires admin)
GET    /api/v1/public-settings                            -- subset; clientSecret stripped
PUT    /api/v1/settings                                   -- [{ name, value }]   (admin)
POST   /api/v1/settings/oidc/test                         -- OidcProviderDetails → OidcTestResult (admin)

GET    /api/v1/admin/oidc-group-mappings                  -- (admin)
POST   /api/v1/admin/oidc-group-mappings                  -- create
PUT    /api/v1/admin/oidc-group-mappings/{id}             -- update
DELETE /api/v1/admin/oidc-group-mappings/{id}             -- delete
```

### 5.3 DTOs

**`OidcCallbackRequest`** ([request.go](booklore-api/internal/oidc/dto/request.go)):

```go
type OidcCallbackRequest struct {
    Code         string `json:"code"         validate:"required"`
    CodeVerifier string `json:"codeVerifier" validate:"required"`
    RedirectURI  string `json:"redirectUri"  validate:"required"`
    Nonce        string `json:"nonce"        validate:"required"`
    State        string `json:"state"        validate:"required"`
}
```

**`OidcProviderDetails`** ([provider_details.go](booklore-api/internal/oidc/dto/provider_details.go)):

```go
type OidcProviderDetails struct {
    ProviderName string        `json:"providerName"`
    ClientID     string        `json:"clientId"`
    ClientSecret string        `json:"clientSecret,omitempty"` // stripped from /public-settings
    IssuerURI    string        `json:"issuerUri"`
    Scopes       string        `json:"scopes,omitempty"`       // space-separated
    ClaimMapping ClaimMapping  `json:"claimMapping"`
}

type ClaimMapping struct {
    Username string `json:"username"`
    Name     string `json:"name"`
    Email    string `json:"email"`
    Groups   string `json:"groups"`
}
```

**`OidcAutoProvisionDetails`** ([auto_provision_details.go](booklore-api/internal/oidc/dto/auto_provision_details.go)):

```go
type OidcAutoProvisionDetails struct {
    EnableAutoProvisioning   bool     `json:"enableAutoProvisioning"`
    AllowLocalAccountLinking bool     `json:"allowLocalAccountLinking"` // default true
    DefaultPermissions       []string `json:"defaultPermissions"`
    DefaultLibraryIDs        []int64  `json:"defaultLibraryIds"`
}
```

**`OidcTestResult`** (from [diagnostic_service.go](booklore-api/internal/oidc/diagnostic_service.go)):

```go
type OidcTestResult struct {
    Success bool             `json:"success"`
    Checks  []OidcTestCheck  `json:"checks"`
}

type CheckStatus string
const (
    CheckPass CheckStatus = "PASS"
    CheckFail CheckStatus = "FAIL"
    CheckWarn CheckStatus = "WARN"
    CheckSkip CheckStatus = "SKIP"
)

type OidcTestCheck struct {
    Name    string      `json:"name"`
    Status  CheckStatus `json:"status"`
    Message string      `json:"message"`
}
```

Validation uses `github.com/go-playground/validator/v10`; JSON (de)serialization uses `encoding/json`.

---

## 6. Backend Logic

### 6.1 Login sequence (`ExchangeCodeForTokens`)

[oidc_auth_service.go:56](booklore-api/internal/oidc/oidc_auth_service.go:56):

1. Verify OIDC is enabled and minimally configured (issuer + client id).
2. `validateRedirectURI(ctx, redirectURI, r)` to block open redirects.
3. `OidcTokenClient.ExchangeAuthorizationCode(ctx, ...)` calls the provider's `token_endpoint` (via `net/http` and `url.Values`) with `grant_type=authorization_code`, `client_id`, `client_secret` (if set), `code`, `redirect_uri`, `code_verifier`.
4. `OidcTokenValidator.ValidateIDToken(ctx, ...)` verifies signature (JWKS), `iss`, `aud`, `azp`, `exp` (30s skew), `iat` (≤5min old), `nonce`, and `at_hash` if `access_token` is present. Built on `github.com/coreos/go-oidc/v3/oidc` + `github.com/lestrrat-go/jwx/v2/jwk` for key-set caching.
5. `OidcTokenClient.FetchUserInfo(ctx, accessToken)` — optional; tolerates missing `userinfo_endpoint` (returns empty `map[string]any`).
6. `OidcClaimExtractor.ExtractClaims(idToken, userinfo, claimMapping)` produces `OidcUserClaims { Username, Email, Name, Subject, PictureURL, Groups }`. Falls back to standard OpenID Connect names, then derives `Username` from `Email` or `Sub`.
7. `FindOrProvisionUser(ctx, ...)` under a per-username `*sync.Mutex` pulled from a `sync.Map` (`usernameLocks`):
   - Match by `(oidc_issuer, oidc_subject)`.
   - Else match by username when `AllowLocalAccountLinking` is true → `LinkExistingUser` (flips `ProvisioningMethod` to `OIDC`, logs `OIDC_ACCOUNT_LINKED`).
   - Else provision a new user when `EnableAutoProvisioning` is true (applies `DefaultPermissions`, `DefaultLibraryIDs`, `ProvisioningMethod = OIDC`, `DefaultPassword = true`; logs `OIDC_USER_PROVISIONED`).
   - Else reject with `OIDC_LOGIN_FAILED`.
8. `SyncExistingUser(...)` refreshes email, name, avatar, `oidc_subject`, `oidc_issuer` as needed; `OidcGroupMappingService.SyncUserGroups(...)` applies group-based permissions according to `OIDC_GROUP_SYNC_MODE`.
9. Persist `OidcSessionModel { UserID, Subject, Issuer, SessionID(sid), IDTokenHint }` for back-channel logout.
10. Issue BookLore access/refresh tokens; access-token lifetime overridden by `OIDC_SESSION_DURATION_HOURS` when set.
11. Audit: `OIDC_LOGIN_SUCCESS`.

Failures log `OIDC_LOGIN_FAILED` with the reason ([oidc_auth_handler.go:56](booklore-api/internal/oidc/oidc_auth_handler.go:56)).

### 6.2 Group sync

[group_mapping_service.go:SyncUserGroups](booklore-api/internal/oidc/group_mapping_service.go:73):

- `DISABLED` — no-op.
- `ON_LOGIN` — find all `OidcGroupMappingModel` rows whose `OidcGroupClaim` is in the user's `groups` claim; **replace** `UserPermissionsModel` and library assignments with the union of those mappings.
- `ON_LOGIN_ADDITIVE` — **merge** the mapping result into the user's existing permissions/libraries (never demotes).

Any mapping with `IsAdmin = true` promotes the user to admin for that login. Removing a mapping or changing groups on the next login reverses it under `ON_LOGIN`; it does not under `ON_LOGIN_ADDITIVE`.

### 6.3 Token validation details

[oidc_token_validator.go](booklore-api/internal/oidc/oidc_token_validator.go):

```go
const (
    ClockSkew      = 30 * time.Second
    MaxIATAge      = 5 * time.Minute
    JWKSCacheTTL   = 6 * time.Hour
    JWKSRefreshTTL = 1 * time.Hour
)
```

- RSA + EC algorithms supported via `jwx` (`jwa.RS256`, `jwa.ES256`).
- JWKS keyset is wrapped in `jwk.NewCache` with auto-refresh; one cache entry per issuer URI.
- Errors are wrapped with `fmt.Errorf("%w", ErrInvalidIDToken)` for typed handling in the handler.

### 6.4 Discovery

[oidc_discovery_service.go](booklore-api/internal/oidc/oidc_discovery_service.go):

- Fetches `${issuerUri}/.well-known/openid-configuration` via `http.Client` with a 10s timeout and caches the `DiscoveryDocument` for 1 hour in a `sync.Map` keyed by issuer URI.
- Exposes `Invalidate(issuerURI string)` — called when settings change so admins don't have to restart.
- The `TestConnection` path uses an uncached discovery fetch so results reflect reality.

### 6.5 Back-channel logout

[backchannel_logout_service.go](booklore-api/internal/oidc/backchannel_logout_service.go):

1. Validate `logout_token` (signature, iss, aud, events claim `http://schemas.openid.net/event/backchannel-logout`).
2. Reject replays via a `processedJTIs` cache (`github.com/patrickmn/go-cache` with 1-hour expiry).
3. Match sessions by `sid`; if absent, match by `(sub, iss)` for "all sessions" logout.
4. Mark `oidc_session.revoked = true`, revoke user's refresh tokens, push a `SESSION_REVOKED` event via the WebSocket hub so connected clients boot themselves.
5. Audit: `BACKCHANNEL_LOGOUT`.

### 6.6 Session cleanup

[session_cleanup.go](booklore-api/internal/oidc/session_cleanup.go) runs every 24h (`time.NewTicker(24*time.Hour)` inside a goroutine managed by the app lifecycle) and deletes:

- revoked sessions older than 7 days,
- any session older than 30 days.

The goroutine honors `ctx.Done()` for graceful shutdown.

### 6.7 Diagnostic (Test Connection)

[diagnostic_service.go:TestConnection](booklore-api/internal/oidc/diagnostic_service.go:32) runs these checks and reports each as PASS / FAIL / WARN / SKIP:

1. Discovery document fetch
2. `authorization_endpoint` present
3. `token_endpoint` present
4. `jwks_uri` present
5. JWKS fetch + key count
6. Required scopes advertised (`openid`, `profile`, `email`)
7. `response_type` `code` supported
8. PKCE `S256` supported
9. `end_session_endpoint` present (logout)
10. `backchannel_logout_supported`

Audit: `OIDC_CONNECTION_TEST`.

---

## 7. Data Model

### 7.1 `oidc_session` (migration `0127_add_oidc_schema.up.sql`)

```sql
CREATE TABLE oidc_session (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            BIGINT      NOT NULL,
    oidc_subject       VARCHAR(255) NOT NULL,
    oidc_issuer        VARCHAR(512) NOT NULL,
    oidc_session_id    VARCHAR(255),          -- sid claim
    id_token_hint      TEXT,                  -- raw ID token for RP-init logout
    created_at         TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_refreshed_at  TIMESTAMP   NULL,
    revoked            BOOLEAN     NOT NULL DEFAULT FALSE,
    CONSTRAINT fk_oidc_session_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_oidc_session_user     ON oidc_session(user_id);
CREATE INDEX idx_oidc_session_subject  ON oidc_session(oidc_subject);
CREATE INDEX idx_oidc_session_sid      ON oidc_session(oidc_session_id);
CREATE INDEX idx_oidc_session_lookup   ON oidc_session(oidc_subject, oidc_issuer, revoked);
```

### 7.2 `users` (additive columns in 0127)

| Column | Type | Notes |
|--------|------|-------|
| `oidc_subject` | `VARCHAR(255)` | From `sub` claim. |
| `oidc_issuer` | `VARCHAR(512)` | From `iss` claim. |
| `avatar_url` | `VARCHAR(1024)` | From `picture` claim. |
| Unique index | `(oidc_issuer, oidc_subject)` | Partial index `WHERE oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL` so multiple local users can have both NULL. |

`provisioning_method` (string: `LOCAL`, `OIDC`, …) is set to `OIDC` on provision and on link.

### 7.3 `oidc_group_mapping` (migration `0128_create_oidc_group_mapping.up.sql`)

```sql
CREATE TABLE oidc_group_mapping (
    id                BIGSERIAL PRIMARY KEY,
    oidc_group_claim  VARCHAR(255) NOT NULL UNIQUE,
    is_admin          BOOLEAN      NOT NULL DEFAULT FALSE,
    permissions       JSONB,           -- array of permission strings
    library_ids       JSONB,           -- array of int64
    description       VARCHAR(500),
    created_at        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### 7.4 GORM models

- [OidcSessionModel](booklore-api/internal/oidc/model/oidc_session_model.go) — struct tags: `gorm:"column:oidc_subject"`, `gorm:"column:revoked;default:false"`, etc. `User` relation via `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`.
- [OidcGroupMappingModel](booklore-api/internal/oidc/model/oidc_group_mapping_model.go) — `BeforeCreate` and `BeforeUpdate` hooks stamp `CreatedAt` / `UpdatedAt`. `Permissions` and `LibraryIDs` use a custom `datatypes.JSON` type with `Scan`/`Value`.
- [BookloreUserModel](booklore-api/internal/user/model/user_model.go) — OIDC columns added in 0127.

---

## 8. Frontend

### 8.1 Login page ([login.component.ts](booklore-ui/src/app/shared/components/login/login.component.ts))

- Reads `oidcEnabled`, `oidcProviderDetails.providerName`, `oidcForceOnlyMode` from `PublicAppSettings`.
- Renders "Sign in with {providerName}" button when OIDC is enabled.
- `loginWithOidc()`:
  1. Guard re-entry via `isOidcLoginInProgress`.
  2. `generatePkce()` → `{code_verifier, code_challenge (S256)}`.
  3. `GET /api/v1/auth/oidc/state` → `state`.
  4. `generateRandomString()` → `nonce`.
  5. `sessionStorage[oidc_pkce_${state}] = { codeVerifier, state, nonce }`.
  6. `buildAuthUrl(...)` resolves `authorization_endpoint` via discovery, builds a URL with `response_type=code`, `client_id`, `redirect_uri=${origin}/oauth2-callback`, `scope`, `code_challenge`, `code_challenge_method=S256`, `state`, `nonce`.
  7. `window.location.href = authUrl`.
- OIDC-only mode ([login.component.ts:90](booklore-ui/src/app/shared/components/login/login.component.ts:90)): if `oidcForceOnlyMode` and there is no `oidcError` query param, auto-redirect. Capped at 3 auto-redirects to avoid loops; `?local=true` bypass escape-hatches to the local form.
- Error handling maps `?oidcError=` to i18n keys (`stateMismatch`, `exchangeFailed`, `userNotProvisioned`, `providerUnreachable`, `invalidToken`, `unknown`). A logout query param `reason=session_revoked` renders an info message.

### 8.2 Callback page ([oidc-callback.component.ts](booklore-ui/src/app/core/security/oidc-callback/oidc-callback.component.ts))

Registered as the public route `/oauth2-callback` ([app.routes.ts:31](booklore-ui/src/app/app.routes.ts:31)).

Steps:
1. Read `code` and `state` from query string.
2. Pull `{codeVerifier, state, nonce}` from `sessionStorage` keyed by state; delete after read.
3. Verify state matches.
4. `POST /api/v1/auth/oidc/callback`.
5. Store tokens, open WebSocket, then redirect to `/change-password` if `isDefaultPassword`, else `/dashboard`.
6. Any failure → `/login?oidcError=<code>`.

### 8.3 Settings page ([authentication-settings.component.ts](booklore-ui/src/app/core/security/oauth2-management/authentication-settings.component.ts))

Panels: Auth methods (toggle) → Provider config → Claim mapping → Auto provisioning → Session duration → Group sync (mode + mappings table + dialog) → Info panel → Test connection dialog.

Each save button patches a single setting through `AppSettingsService.saveSettings([{name, value}])` and shows a toast. Test connection calls `AppSettingsService.testOidcConnection(providerDetails)` and renders the returned checks in a dialog.

### 8.4 Services & models

- [oidc.service.ts](booklore-ui/src/app/core/security/oidc.service.ts) — PKCE, state, auth URL, token exchange, session-storage helpers.
- [app-settings.service.ts](booklore-ui/src/app/shared/service/app-settings.service.ts) — `appSettings$`, `publicAppSettings$`, `saveSettings`, `toggleOidcEnabled`, `testOidcConnection`.
- [oidc-group-mapping.service.ts](booklore-ui/src/app/shared/service/oidc-group-mapping.service.ts) — CRUD for `/api/v1/admin/oidc-group-mappings`.
- Models: [app-settings.model.ts](booklore-ui/src/app/shared/model/app-settings.model.ts) (`OidcProviderDetails`, `OidcAutoProvisionDetails`, `OidcTestResult`, `OidcTestCheck`), [oidc-group-mapping.model.ts](booklore-ui/src/app/shared/model/oidc-group-mapping.model.ts).

---

## 9. Audit

All OIDC-relevant changes write an audit row. Actions on [audit_action.go](booklore-api/internal/audit/enum/audit_action.go):

| Action | Trigger |
|--------|---------|
| `OIDC_CONFIG_CHANGED` | Any OIDC provider/auto-provision/session/sync setting updated |
| `OIDC_FORCE_ONLY_MODE_CHANGED` | Force-only toggle |
| `OIDC_CONNECTION_TEST` | Admin ran Test Connection |
| `OIDC_LOGIN_SUCCESS` | Successful `ExchangeCodeForTokens` |
| `OIDC_LOGIN_FAILED` | Error in callback (state mismatch, token exchange failed, invalid ID token, unprovisioned user, etc.) |
| `OIDC_USER_PROVISIONED` | New user created on first OIDC login |
| `OIDC_ACCOUNT_LINKED` | Local user linked to OIDC identity |
| `OIDC_GROUP_MAPPING_CREATED` / `_UPDATED` / `_DELETED` | Group mapping CRUD |
| `BACKCHANNEL_LOGOUT` | Successful back-channel logout |

---

## 10. Security Considerations

- **PKCE (S256)** is mandatory — the code_verifier is generated client-side, never stored server-side, and the provider enforces the binding.
- **State token** is server-generated, single-use, 5-minute TTL (`go-cache` in [oidc_state_service.go](booklore-api/internal/oidc/oidc_state_service.go)). CSRF on the callback is blocked without it. The 32 random bytes come from `crypto/rand.Read` and are encoded with `base64.RawURLEncoding`.
- **Open-redirect guard** in `validateRedirectURI` rejects anything that isn't `booklore://oauth2-callback` or an `http(s)` URL ending in `/oauth2-callback` with a matching origin. Uses `net/url.Parse` + `strings.HasSuffix`.
- **ID token validation** — signature via remote JWKS (RSA/EC); `iss`, `aud`, `azp`, `exp`, `iat` (≤5 min old), `nonce`, and `at_hash` are all verified.
- **Replay protection** — back-channel `logout_token`s are tracked by `jti` in a 1-hour `go-cache` instance.
- **Race-condition safety** on first login — a `sync.Map` of `*sync.Mutex` keyed by username prevents two concurrent callbacks from creating duplicate users. The lock is `Defer`ed via `defer mu.Unlock()` to guarantee release on panic.
- **Client secret at rest** — stored **unencrypted** in `app_settings`. Mitigations: never returned from `/public-settings` ([AppSettingService.buildPublicSetting()](booklore-api/internal/appsettings/service.go:148) strips it); admin-only read on the full settings endpoint; DB-level controls and TLS expected in production. Application-level encryption is open work.
- **Force-only mode guard** — server refuses to enable `OIDC_FORCE_ONLY_MODE` unless OIDC is enabled and `IssuerURI` + `ClientID` are present, so an admin can't lock themselves out with a misconfigured provider.
- **Session revocation** — back-channel logout marks `oidc_session.revoked`, revokes refresh tokens, and pushes a `SESSION_REVOKED` event through the WebSocket hub (`gorilla/websocket`) to connected clients.
- **Context propagation** — every outbound HTTP call (token, userinfo, JWKS, discovery) is made with `ctx` derived from the incoming request so slow providers cannot tie up goroutines past client disconnect or handler timeout.

---

## 11. Edge Cases

| Case | Outcome |
|------|---------|
| OIDC disabled but callback hit | Rejected; request returns `OIDC_LOGIN_FAILED`. |
| `state` missing, expired, or already consumed | 4xx; UI shows `stateMismatch`. |
| Redirect URI not in allow-list | Rejected by `validateRedirectURI`. |
| Token exchange returns an `error` payload | `ErrOidcTokenExchangeFailed`; audit `OIDC_LOGIN_FAILED`. |
| ID token signature invalid / `iss` mismatch / `nonce` mismatch | Rejected in `OidcTokenValidator`. |
| `iat` older than 5 minutes | Rejected (anti-replay). |
| `userinfo_endpoint` missing from discovery | Treated as empty map; claims come from ID token only. |
| Unknown OIDC subject, auto-provision off, linking off | Login rejected. |
| Unknown OIDC subject, linking on, username collides with local user | Local user is linked and `ProvisioningMethod` flips to `OIDC`. |
| Two simultaneous first-logins for the same user | Serialized by per-username `*sync.Mutex`; only one provision occurs. |
| Group claim absent | Group sync produces no mappings; user keeps existing perms (both modes). |
| `ON_LOGIN` removes all mappings for a user | Perms/libs shrink to the auto-provision defaults (or empty if no match). |
| `ON_LOGIN_ADDITIVE` after a group is removed upstream | Perms/libs do **not** shrink; operator must clear manually. |
| Provider issues no `offline_access` | No refresh token; BookLore JWT refresh still works via its own refresh token. |
| `oidcForceOnlyMode` on + provider down | Admin can still reach local login via `/login?local=true`. |
| `OidcSessionModel` retained indefinitely | Cleaned up by the [session cleanup goroutine](booklore-api/internal/oidc/session_cleanup.go) (revoked > 7d, any > 30d). |
| Shutdown during an in-flight callback | Parent `ctx` cancels outbound HTTP calls; handler returns 503; no partial user row because provisioning happens in a `db.Transaction`. |

---

## 12. Validation Summary

| Layer | Rule |
|-------|------|
| UI | `providerName`, `clientId`, `issuerUri`, and all three required claim mappings (`username`, `email`, `name`) must be present before OIDC can be enabled. |
| DTO | `OidcCallbackRequest` — `validate:"required"` on every field. |
| Service | `validateOidcForceOnlyMode` blocks force-only without a usable OIDC config; `validateRedirectURI` guards against open redirects. |
| Token | Signature, iss, aud, azp, exp, iat, nonce, at_hash all checked on every login. |
| DB | `users (oidc_issuer, oidc_subject)` partial unique index; `oidc_group_mapping.oidc_group_claim` unique. |
| Auth | All OIDC settings and group-mapping endpoints require ADMIN middleware. |

---

## 13. Configuration Examples

Authentik provider:

```json
{
  "providerName": "Authentik",
  "clientId": "booklore",
  "clientSecret": "***",
  "issuerUri": "https://auth.example.com/application/o/booklore/",
  "scopes": "openid profile email groups offline_access",
  "claimMapping": {
    "username": "preferred_username",
    "email": "email",
    "name": "given_name",
    "groups": "groups"
  }
}
```

Auto provisioning:

```json
{
  "enableAutoProvisioning": true,
  "allowLocalAccountLinking": true,
  "defaultPermissions": ["permissionRead", "permissionDownload"],
  "defaultLibraryIds": [1, 2]
}
```

Group mapping (librarians → admin of libraries 1–3):

```json
{
  "oidcGroupClaim": "librarians",
  "isAdmin": true,
  "permissions": [
    "permissionRead", "permissionUpload", "permissionDownload",
    "permissionEditMetadata", "permissionManageLibrary"
  ],
  "libraryIds": [1, 2, 3],
  "description": "Library staff with full access"
}
```

---

## 14. Open / Future Work

1. Encrypt `ClientSecret` at rest (currently plaintext in `app_settings`) — either column-level via `crypto/aes-gcm` with a KEK from env, or move to an external secrets store.
2. Server-side issuer/scope sanity check on save (today: only the explicit Test Connection button).
3. Admin-triggered "re-sync groups now" without requiring the user to re-login.
4. Optional `ON_LOGIN_STRICT` mode that shrinks perms in an additive world after N days of inactivity.
5. Support multiple OIDC providers simultaneously (currently one).
6. Configurable clock skew and `iat` age, rather than the hard-coded 30s / 300s constants.
7. Expose back-channel logout test from the diagnostic panel (sign a dummy logout_token).
8. Replace the per-username `sync.Map` lock registry with a `singleflight.Group` to collapse duplicate in-flight provisions automatically.

---

## 15. Key References

- Handlers: [oidc_auth_handler.go](booklore-api/internal/oidc/oidc_auth_handler.go), [group_mapping_handler.go](booklore-api/internal/oidc/group_mapping_handler.go), [app_setting_handler.go](booklore-api/internal/appsettings/handler.go)
- Services: [oidc_auth_service.go](booklore-api/internal/oidc/oidc_auth_service.go), [oidc_token_client.go](booklore-api/internal/oidc/oidc_token_client.go), [oidc_token_validator.go](booklore-api/internal/oidc/oidc_token_validator.go), [oidc_discovery_service.go](booklore-api/internal/oidc/oidc_discovery_service.go), [oidc_claim_extractor.go](booklore-api/internal/oidc/oidc_claim_extractor.go), [oidc_state_service.go](booklore-api/internal/oidc/oidc_state_service.go), [backchannel_logout_service.go](booklore-api/internal/oidc/backchannel_logout_service.go), [diagnostic_service.go](booklore-api/internal/oidc/diagnostic_service.go), [group_mapping_service.go](booklore-api/internal/oidc/group_mapping_service.go), [user_provisioning_service.go](booklore-api/internal/user/provisioning_service.go), [appsettings/service.go](booklore-api/internal/appsettings/service.go)
- Models: [oidc_session_model.go](booklore-api/internal/oidc/model/oidc_session_model.go), [oidc_group_mapping_model.go](booklore-api/internal/oidc/model/oidc_group_mapping_model.go), [user_model.go](booklore-api/internal/user/model/user_model.go)
- DTOs: [request.go](booklore-api/internal/oidc/dto/request.go), [provider_details.go](booklore-api/internal/oidc/dto/provider_details.go), [auto_provision_details.go](booklore-api/internal/oidc/dto/auto_provision_details.go)
- Enums/constants: [app_setting_key.go](booklore-api/internal/appsettings/enum/app_setting_key.go), [audit_action.go](booklore-api/internal/audit/enum/audit_action.go)
- Security middleware: [security.go](booklore-api/internal/security/middleware.go)
- Cleanup goroutine: [session_cleanup.go](booklore-api/internal/oidc/session_cleanup.go)
- Migrations: `0127_add_oidc_schema.up.sql`, `0128_create_oidc_group_mapping.up.sql`
- Third-party libraries: `github.com/coreos/go-oidc/v3`, `github.com/lestrrat-go/jwx/v2`, `github.com/go-playground/validator/v10`, `github.com/patrickmn/go-cache`, `gorm.io/gorm` + `gorm.io/datatypes`, `github.com/gorilla/websocket`, `golang-migrate/migrate`.
- UI login: [login.component.ts](booklore-ui/src/app/shared/components/login/login.component.ts), [oidc-callback.component.ts](booklore-ui/src/app/core/security/oidc-callback/oidc-callback.component.ts), [oidc.service.ts](booklore-ui/src/app/core/security/oidc.service.ts), [app.routes.ts:31](booklore-ui/src/app/app.routes.ts:31)
- UI settings: [authentication-settings.component.ts](booklore-ui/src/app/core/security/oauth2-management/authentication-settings.component.ts), [authentication-settings.component.html](booklore-ui/src/app/core/security/oauth2-management/authentication-settings.component.html)
- UI services: [app-settings.service.ts](booklore-ui/src/app/shared/service/app-settings.service.ts), [oidc-group-mapping.service.ts](booklore-ui/src/app/shared/service/oidc-group-mapping.service.ts)
- UI models: [app-settings.model.ts](booklore-ui/src/app/shared/model/app-settings.model.ts), [oidc-group-mapping.model.ts](booklore-ui/src/app/shared/model/oidc-group-mapping.model.ts)
