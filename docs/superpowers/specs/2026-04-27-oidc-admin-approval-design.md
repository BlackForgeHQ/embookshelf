# OIDC Admin Approval — Design

**Date:** 2026-04-27
**Status:** Draft

## Problem

When OIDC auto-provisioning is enabled, any identity from a configured provider that successfully completes the OIDC flow gets a usable embookshelf account immediately. For self-hosted instances exposed to a wide identity provider (e.g. a corporate Google Workspace, a public OIDC issuer, or a personal SSO that has more accounts than the admin wants in their library), this is too permissive.

Admins want a third state: **auto-create the user, but hold the account in a "pending" state until an admin approves it.** The user cannot log in until approval.

## Goals

- Admins can require manual approval for newly auto-provisioned OIDC users.
- New users land on a clear "pending approval" page; they get no session.
- Admins approve or deny pending users from the existing Users & roles settings panel.
- Denied users cannot keep retrying — denial is durable.
- Existing installs upgrade with no behavior change (default off).

## Non-goals

- Email notifications to admins.
- Full audit log of approval decisions (only `status_changed_at` is recorded).
- TTL / auto-cleanup of stale pending users.
- A general "disable user" flow.
- Per-provider approval policy — this is a single global flag.

## Configuration

Extend `OIDCAutoProvisionDetails` in `internal/repo/app_settings.go` with one new field:

```go
type OIDCAutoProvisionDetails struct {
    EnableAutoProvisioning   bool   `json:"enableAutoProvisioning"`
    AllowLocalAccountLinking bool   `json:"allowLocalAccountLinking"`
    DefaultRole              string `json:"defaultRole"`
    RequireAdminApproval     bool   `json:"requireAdminApproval"` // NEW
}
```

- Default: `false`. Existing installs see no behavior change.
- `RequireAdminApproval` is only meaningful when `EnableAutoProvisioning` is `true`. When auto-provisioning is off, unknown OIDC users are still rejected outright (current behavior).
- Settings UI: a third checkbox under the existing two in the OIDC Auto-Provisioning settings panel, conditionally enabled when auto-provisioning is on. Helper text explains that flipping the flag off does not auto-promote already-pending users.

## Data model

New migration `000023_user_approval_status`:

```sql
-- up
ALTER TABLE users
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'pending', 'denied'));
ALTER TABLE users
    ADD COLUMN status_changed_at TIMESTAMPTZ;

-- down
ALTER TABLE users DROP COLUMN status_changed_at;
ALTER TABLE users DROP COLUMN status;
```

- All existing rows backfill to `'active'`.
- `status_changed_at` lets the admin UI sort the pending queue oldest-first.

Extend `model.User` in `internal/model/user.go`:

```go
type UserStatus string

const (
    UserStatusActive  UserStatus = "active"
    UserStatusPending UserStatus = "pending"
    UserStatusDenied  UserStatus = "denied"
)

type User struct {
    // existing fields...
    Status          UserStatus
    StatusChangedAt *time.Time
}
```

Pending and denied states are produced **only** by OIDC auto-provisioning. Local-bootstrap signup, admin user-creation, and the email-link path always result in `active` users.

## Service & flow

### `OIDCService.findOrProvisionUser` (`internal/service/oidc.go`)

- **Path 1 — match by OIDC identity:** check `user.Status`.
  - `active` → return user (current behavior).
  - `pending` → return new sentinel `ErrOIDCPendingApproval` along with the user.
  - `denied` → return existing sentinel `ErrOIDCLoginNotAllowed`.
- **Path 2 — match by email, link OIDC:** unchanged. Local users are already `active`.
- **Path 3 — auto-provision:** if `RequireAdminApproval = true`, call new repo method `CreateOIDCPending(...)` that inserts with `status='pending'`, `status_changed_at=now()`, and return `ErrOIDCPendingApproval`. Otherwise current `CreateOIDC` path.
- The first-user-becomes-admin shortcut at `internal/service/oidc.go:647` bypasses approval. First user is always `active` admin — otherwise an admin-less instance with approval-required would be unrecoverable.

### `OIDCService.Exchange`

- Wrap the `findOrProvisionUser` call. If `ErrOIDCPendingApproval` bubbles up:
  - Do **not** call `SyncOIDCProfile`, `sessions.Create`, or `TouchLastSeen`.
  - Return the sentinel to the handler. No `model.Session` is produced.

### Handler `OIDCCallback` (`internal/handler/oidc.go:54`)

- On `ErrOIDCPendingApproval`, redirect to `/login/pending` (HTTP 302).
- All other errors keep the existing `/login?oidcError=...` redirect.

### New user-management service methods

Mirror existing admin user CRUD:

- `ApproveUser(ctx, adminID, userID) error` — flips `pending → active` or `denied → active`, sets `status_changed_at`, emits SSE invalidation event for the users query. Idempotent for already-`active`.
- `DenyUser(ctx, adminID, userID) error` — flips `pending → denied`, sets `status_changed_at`, emits SSE event. Idempotent for already-`denied`.
- Both are admin-only.
- Both refuse to act on the calling admin's own row (cannot self-deny).
- Deny refuses to act on the last remaining admin user (cannot lock the instance).

## API

New admin endpoints under `/api/v1/settings/users`:

- `POST /api/v1/settings/users/:id/approve` → 200 with updated user DTO. Idempotent for already-`active`. 404 if no such user. 403 if caller is not admin.
- `POST /api/v1/settings/users/:id/deny` → 200 with updated user DTO. Idempotent for already-`denied`. 400 if denying self or last admin. 404 if no such user. 403 if caller is not admin.

Existing `GET /api/v1/settings/users` response: extend each user DTO with:

```json
{
  "status": "pending|active|denied",
  "statusChangedAt": "2026-04-27T12:34:56Z"
}
```

No separate "list pending" endpoint — pending users are a subset of the existing list, filtered/grouped client-side.

Realtime: emit an SSE event on `/events` when a user's status changes (re-use the existing settings-users invalidation event if present, or add one), so any open admin tab updates the badge and list without polling.

## Frontend

### New route — `ui/src/routes/login.pending.tsx`

Reachable at `/login/pending`. No auth required. Renders a calm message:

> **Pending approval**
> Your account has been created and is awaiting approval from an administrator. You will be able to sign in once it's reviewed. You can close this tab.

Single "Back to login" link. No automatic polling, no retry — the user comes back later via the normal login flow.

### Login route

No changes. The OIDC callback redirects directly to `/login/pending` server-side.

### Users panel — `ui/src/routes/_app.settings.tsx` (`UsersPanel`)

- Each user row shows a status pill:
  - `active` → no pill (avoid clutter).
  - `pending` → amber "Pending" pill.
  - `denied` → gray "Denied" pill.
- **Pending rows** show Approve (primary) and Deny (ghost) buttons inline. Edit/delete are hidden until approved.
- **Denied rows** show a single Approve button (re-enabling). Delete remains available.
- **Active rows:** existing edit/delete UI, unchanged.
- **Sort order:** pending first (oldest first by `statusChangedAt`), then active, then denied.

### Settings nav badge

The "Users & roles" tab in the settings sidebar gets a small numeric badge when pending count > 0. The count is derived client-side from the existing users query — no extra endpoint. The badge updates reactively via SSE invalidation.

### API client

Add to the appropriate module (`ui/src/api/settings-users.ts` or wherever `fetchSettingsUsers` lives):

- `approveUser(id: string): Promise<UserDTO>`
- `denyUser(id: string): Promise<UserDTO>`

Wire as TanStack Query mutations that invalidate `settingsUsersQueryKey` on success.

## Edge cases

| Scenario | Behavior |
| --- | --- |
| First user ever, approval required + force-only + auto-provision on | First-user shortcut wins: `active` admin, no approval needed. |
| Toggle flipped on retroactively | Existing users untouched. Only **new** auto-provisioned users land in `pending`. |
| Toggle flipped off while pending users exist | Pending rows stay pending. Admin still needs to approve or deny — they don't auto-promote. Helper text in the UI explains this. |
| Denied user retries via OIDC | Path 1 matches the existing OIDC link, sees `status='denied'`, returns `ErrOIDCLoginNotAllowed`. No new pending row. No retry storm. |
| Approve a denied user | Status flips to `active`. Next OIDC login succeeds normally. |
| Admin tries to deny themself | Service rejects with a clear error. UI hides the Deny button on the admin's own row. |
| Admin tries to deny last remaining admin | Service rejects with a clear error. |
| Email-match path (path 2) for an already-pending user | Cannot happen — pending users are created by OIDC and already have `oidc_subject` set; path 2 only fires when the local user has no OIDC link. |

## Testing

- **Go unit tests** in `internal/service/oidc_test.go`: each path × each status × `RequireAdminApproval` on/off.
- **Go unit tests** for `ApproveUser` / `DenyUser` in the user service: success, idempotency, last-admin guard, self-deny guard.
- **Handler tests** for `/api/v1/auth/oidc/callback`: redirects to `/login/pending` on the pending sentinel.
- **Repo test** for migration round-trip; existing rows backfill to `active`.
- **E2E (Playwright)**: admin enables approval requirement → simulated OIDC login → user lands on `/login/pending` → admin sees pending badge in settings → admin approves → user can log in. (If no mock OIDC provider exists in the e2e harness, fall back to a Go-level integration test for the auth half and a Playwright test for the admin half.)
- **Vitest** for the Users panel: pending row renders Approve/Deny, denied row renders Approve only, active row unchanged.

## Implementation order (rough)

1. Migration + model + repo changes (`status`, `status_changed_at`, `CreateOIDCPending`, status-aware getters).
2. Service: `ErrOIDCPendingApproval`, `findOrProvisionUser` branches, `Exchange` wiring.
3. Handler: callback redirect to `/login/pending`.
4. Service: `ApproveUser` / `DenyUser` + admin guards.
5. API endpoints + DTO extension.
6. SSE invalidation event.
7. Frontend: `/login/pending` route.
8. Frontend: Users panel pills, buttons, sort, badge.
9. Settings UI: third checkbox + helper text.
10. Tests across the stack.
