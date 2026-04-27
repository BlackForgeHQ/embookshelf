# OIDC Admin Approval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `RequireAdminApproval` flag to OIDC auto-provisioning that holds new users in a `pending` status until an admin approves them via the existing Users & roles settings panel.

**Architecture:** Adds a `status` enum column to `users` (`active | pending | denied`). Extends `OIDCAutoProvisionDetails` with `RequireAdminApproval`. The OIDC `Exchange` returns a new sentinel `ErrOIDCPendingApproval` that the callback handler turns into a redirect to a new `/login/pending` page. Two new admin endpoints (`/users/:id/approve`, `/users/:id/deny`) flip the status. The Users & roles panel renders a status pill, approve/deny buttons, and a numeric badge fed by an SSE invalidation event. Existing rows backfill to `active`, so upgrades are zero-impact.

**Tech Stack:** Go 1.25 (Gin, pgx, golang-migrate), React 19 (TanStack Router/Query, shadcn/ui), Vitest, Playwright.

**Spec:** [docs/superpowers/specs/2026-04-27-oidc-admin-approval-design.md](../specs/2026-04-27-oidc-admin-approval-design.md)

---

## File Structure

**Backend (Go):**
- Create: `internal/migrator/migrations/000023_user_approval_status.up.sql`
- Create: `internal/migrator/migrations/000023_user_approval_status.down.sql`
- Modify: `internal/model/user.go` — add `UserStatus` type and fields on `User`.
- Modify: `internal/repo/user.go` — extend column list, add `CreateOIDCPending`, `UpdateStatus`, status filter helpers; update `scanUser`.
- Modify: `internal/repo/app_settings.go` — add `RequireAdminApproval` field + default + validation.
- Modify: `internal/service/oidc.go` — new sentinel `ErrOIDCPendingApproval`; status-aware branch logic in `findOrProvisionUser`; pending users skip session creation in `Exchange`.
- Modify: `internal/service/auth.go` — add `ApproveUser` and `DenyUser` with last-admin / self-target guards. Inject `*sse.Hub` so they can broadcast.
- Create: `internal/service/auth_test.go` — in-memory fake `userStatusRepo` covering approve/deny guards.
- Modify: `cmd/embookshelf/main.go` — pass `hub` to `NewAuthService`.
- Modify: `internal/handler/auth.go` — extend `userDTO` with `status` and `statusChangedAt`; map in `toUserDTO`.
- Modify: `internal/handler/oidc.go` — redirect to `/login/pending` on `ErrOIDCPendingApproval`; extend `oidcErrorCode` mapping.
- Create: `internal/handler/oidc_test.go` — pure-function test for `oidcErrorCode`.
- Modify: `internal/handler/users.go` — add `SettingsUsersApprove` and `SettingsUsersDeny` handlers.
- Modify: `internal/handler/router.go` — wire the two new admin routes.

**Frontend (TS/React):**
- Create: `ui/src/routes/login.pending.tsx` — static "pending approval" page.
- Modify: `ui/src/api/auth.ts` — extend `AuthUser` with `status` and `statusChangedAt`.
- Modify: `ui/src/api/oidc.ts` — extend `OidcAutoProvision` with `requireAdminApproval`.
- Modify: `ui/src/api/settings.ts` — add `approveSettingsUser`, `denySettingsUser`.
- Modify: `ui/src/api/realtime.ts` — wire `users.updated` event to invalidate the users query.
- Modify: `ui/src/routes/_app.settings.tsx` — third checkbox in OIDC auto-provisioning panel; pending/denied pills, Approve/Deny buttons, sort, and pending-count badge in the Users panel.
- Create: `ui/src/components/__tests__/UserStatusBadge.test.tsx` — Vitest for the new pill component (if extracted) or for the Users panel.

**E2E:**
- Create: `e2e/fixtures/sql/pending-user.sql` — inserts a deterministic pending user.
- Create: `e2e/tests/admin-approval.spec.ts` — admin sees badge, approves, denies; user list reflects state.

---

## Task 1: Migration — `users.status` and `users.status_changed_at`

**Files:**
- Create: `internal/migrator/migrations/000023_user_approval_status.up.sql`
- Create: `internal/migrator/migrations/000023_user_approval_status.down.sql`

- [ ] **Step 1: Write the up migration**

`internal/migrator/migrations/000023_user_approval_status.up.sql`:
```sql
ALTER TABLE users
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'pending', 'denied'));

ALTER TABLE users
    ADD COLUMN status_changed_at TIMESTAMPTZ;

CREATE INDEX users_status_idx ON users (status) WHERE status <> 'active';
```

- [ ] **Step 2: Write the down migration**

`internal/migrator/migrations/000023_user_approval_status.down.sql`:
```sql
DROP INDEX IF EXISTS users_status_idx;
ALTER TABLE users DROP COLUMN IF EXISTS status_changed_at;
ALTER TABLE users DROP COLUMN IF EXISTS status;
```

- [ ] **Step 3: Apply and verify round-trip**

Run:
```
make migrate
make migrate-version
make migrate-down
make migrate
```

Expected output: `make migrate-version` prints `23` after the up; `make migrate-down` rolls back to `22`; the second `make migrate` reapplies cleanly.

- [ ] **Step 4: Sanity-check column with psql**

Run:
```
docker exec -i $(docker compose -f compose.dev.yml ps -q postgres-embookshelf) psql -U embookshelf -d embookshelf -c "\d users"
```

Expected: `status` column with default `'active'`, NOT NULL; `status_changed_at` column TIMESTAMPTZ nullable; `users_status_idx` partial index listed.

- [ ] **Step 5: Commit**

```bash
git add internal/migrator/migrations/000023_user_approval_status.up.sql internal/migrator/migrations/000023_user_approval_status.down.sql
git commit -m "feat(db): add users.status for OIDC admin approval"
```

---

## Task 2: Model — `UserStatus` enum + struct fields

**Files:**
- Modify: `internal/model/user.go`

- [ ] **Step 1: Add UserStatus type and constants**

Open `internal/model/user.go`. Above the `User` struct (after the `Role` block ending at line 10), insert:

```go
type UserStatus string

const (
	UserStatusActive  UserStatus = "active"
	UserStatusPending UserStatus = "pending"
	UserStatusDenied  UserStatus = "denied"
)
```

- [ ] **Step 2: Extend the User struct**

In the `User` struct (lines 12–24), add two fields just before `CreatedAt`:

```go
	Status          UserStatus
	StatusChangedAt *time.Time
```

- [ ] **Step 3: Build to confirm compilation**

Run: `go build ./...`
Expected: build fails inside `internal/repo/user.go` because `scanUser` no longer matches the struct shape — that failure is fixed in Task 3.

- [ ] **Step 4: Commit**

```bash
git add internal/model/user.go
git commit -m "feat(model): add UserStatus enum and User.Status fields"
```

---

## Task 3: Repo — extend `users` queries for status

**Files:**
- Modify: `internal/repo/user.go`

- [ ] **Step 1: Extend the column list and scanner**

In `internal/repo/user.go`, update line 23:

```go
const userCols = `id, email, password_hash, name, role, oidc_subject, oidc_issuer, avatar_url, status, status_changed_at, created_at, updated_at, last_seen_at`
```

Update `scanUser` (lines 184–198) to read the two new columns. Replace the function body with:

```go
func scanUser(s scanner) (model.User, error) {
	var (
		u      model.User
		role   string
		status string
	)
	err := s.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role,
		&u.OIDCSubject, &u.OIDCIssuer, &u.AvatarURL,
		&status, &u.StatusChangedAt,
		&u.CreatedAt, &u.UpdatedAt, &u.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.Role = model.Role(role)
	u.Status = model.UserStatus(status)
	return u, nil
}
```

- [ ] **Step 2: Add `CreateOIDCPending`**

Append below the existing `CreateOIDC` (after line 158):

```go
// CreateOIDCPending mirrors CreateOIDC but inserts the user with
// status='pending' so they cannot log in until an admin approves them.
func (r *UserRepo) CreateOIDCPending(ctx context.Context, email, name string, role model.Role, issuer, subject string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, oidc_issuer, oidc_subject, status, status_changed_at)
		VALUES (lower($1), $2, '', $3, $4, $5, 'pending', now())
		RETURNING `+userCols+`
	`, strings.TrimSpace(email), strings.TrimSpace(name), string(role), issuer, subject)
	return scanUser(row)
}
```

- [ ] **Step 3: Add `UpdateStatus`**

Append below `CreateOIDCPending`:

```go
// UpdateStatus flips the approval status. The caller (service) enforces
// guards (last admin, self-target) before calling this.
func (r *UserRepo) UpdateStatus(ctx context.Context, id string, status model.UserStatus) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users
		SET status = $2, status_changed_at = now(), updated_at = now()
		WHERE id = $1
	`, id, string(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: PASS. (Other modules now compile; service layer still uses old `CreateOIDC` everywhere — that's fine.)

- [ ] **Step 5: Run unit tests touching repo signatures**

Run: `go test ./...`
Expected: PASS for the existing pure-logic packages; the project has no Go tests that hit the DB so nothing breaks.

- [ ] **Step 6: Commit**

```bash
git add internal/repo/user.go
git commit -m "feat(repo): scan user status; add CreateOIDCPending and UpdateStatus"
```

---

## Task 4: Settings — add `RequireAdminApproval`

**Files:**
- Modify: `internal/repo/app_settings.go`

- [ ] **Step 1: Extend the struct + default**

In `internal/repo/app_settings.go`, replace the `OIDCAutoProvisionDetails` struct (lines 89–93) with:

```go
type OIDCAutoProvisionDetails struct {
	EnableAutoProvisioning   bool   `json:"enableAutoProvisioning"`
	AllowLocalAccountLinking bool   `json:"allowLocalAccountLinking"`
	DefaultRole              string `json:"defaultRole"` // "admin" | "user"
	RequireAdminApproval     bool   `json:"requireAdminApproval"`
}
```

Update `DefaultOIDCAutoProvisionDetails` (lines 111–116) to include the new field:

```go
func DefaultOIDCAutoProvisionDetails() OIDCAutoProvisionDetails {
	return OIDCAutoProvisionDetails{
		EnableAutoProvisioning:   false,
		AllowLocalAccountLinking: true,
		DefaultRole:              "user",
		RequireAdminApproval:     false,
	}
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: PASS — no other code references the new field yet.

- [ ] **Step 3: Commit**

```bash
git add internal/repo/app_settings.go
git commit -m "feat(settings): add RequireAdminApproval to OIDCAutoProvisionDetails"
```

---

## Task 5: Service — `ErrOIDCPendingApproval` + status-aware OIDC flow

**Files:**
- Modify: `internal/service/oidc.go`

- [ ] **Step 1: Add the new sentinel error**

In `internal/service/oidc.go` (line 26 var block), append `ErrOIDCPendingApproval`:

```go
var (
	ErrOIDCNotConfigured    = errors.New("OIDC is not configured")
	ErrOIDCDisabled         = errors.New("OIDC is disabled")
	ErrOIDCStateMismatch    = errors.New("OIDC state mismatch")
	ErrOIDCLoginNotAllowed  = errors.New("this OIDC identity is not allowed to log in")
	ErrOIDCForceOnlyBlocked = errors.New("OIDC-only mode cannot be enabled without at least one configured provider")
	ErrOIDCUnknownProvider  = errors.New("unknown OIDC provider")
	ErrOIDCPendingApproval  = errors.New("this OIDC account is awaiting administrator approval")
)
```

- [ ] **Step 2: Add status checks to path 1 (existing OIDC link)**

In `findOrProvisionUser` (lines 605–655), replace the `// 1) Match by OIDC identity.` block (lines 606–613) with:

```go
	// 1) Match by OIDC identity. Existing users still need to clear
	//    the status gate — pending users have not been approved yet,
	//    denied users have been explicitly refused.
	u, err := s.users.GetByOIDC(ctx, issuer, claims.Subject)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return model.User{}, err
	}
	if err == nil {
		switch u.Status {
		case model.UserStatusActive:
			return u, nil
		case model.UserStatusPending:
			return u, ErrOIDCPendingApproval
		case model.UserStatusDenied:
			return model.User{}, ErrOIDCLoginNotAllowed
		default:
			return u, nil
		}
	}
```

- [ ] **Step 3: Branch path 3 on `RequireAdminApproval`**

In the same function, replace the `// 3) Auto-provision.` block all the way through the final `return s.users.CreateOIDC(...)` (lines 633–654) with:

```go
	// 3) Auto-provision.
	if !provision.EnableAutoProvisioning {
		n, err := s.users.Count(ctx)
		if err != nil {
			return model.User{}, err
		}
		if n > 0 {
			return model.User{}, ErrOIDCLoginNotAllowed
		}
	}

	role := model.RoleUser
	if provision.DefaultRole == "admin" {
		role = model.RoleAdmin
	}
	// First-user-becomes-admin shortcut bypasses approval — otherwise
	// an admin-less instance with approval-required is unrecoverable.
	firstUser := false
	if n, err := s.users.Count(ctx); err == nil && n == 0 {
		role = model.RoleAdmin
		firstUser = true
	}

	if claims.Email == "" {
		return model.User{}, errors.New("OIDC provider did not return an email claim and email is required")
	}

	if provision.RequireAdminApproval && !firstUser {
		created, err := s.users.CreateOIDCPending(ctx, claims.Email, claims.Name, role, issuer, claims.Subject)
		if err != nil {
			return model.User{}, err
		}
		return created, ErrOIDCPendingApproval
	}
	return s.users.CreateOIDC(ctx, claims.Email, claims.Name, role, issuer, claims.Subject)
```

- [ ] **Step 4: Skip session creation for pending users in `Exchange`**

In `Exchange` (lines 228–302), wrap the post-`findOrProvisionUser` block (lines 290–301) so the pending-approval error short-circuits:

```go
	u, err := s.findOrProvisionUser(ctx, issuer, claims, provision)
	if err != nil {
		if errors.Is(err, ErrOIDCPendingApproval) {
			// Return the user so callers (handlers, tests) can see who is
			// pending, but no session is issued.
			return model.Session{}, u, ErrOIDCPendingApproval
		}
		return model.Session{}, model.User{}, err
	}
	_ = s.users.SyncOIDCProfile(ctx, u.ID, claims.Name, claims.Picture)

	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())
	return sess, u, nil
```

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 6: Run existing tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/oidc.go
git commit -m "feat(oidc): pending-approval flow in findOrProvisionUser and Exchange"
```

---

## Task 6: Service — `ApproveUser` / `DenyUser` with TDD via fake repo

**Files:**
- Modify: `internal/service/auth.go`
- Create: `internal/service/auth_test.go`
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Introduce a tiny status-repo seam in `auth.go`**

In `internal/service/auth.go`, just above the existing `AuthService` struct (line 26), add an interface that captures only the methods the new approve/deny logic needs. This keeps the existing struct mostly intact and lets the test substitute a fake.

```go
// userStatusRepo is the slice of UserRepo that ApproveUser and DenyUser
// touch. Defining it as a tiny interface lets the service test substitute
// an in-memory fake without spinning up a database — the rest of
// AuthService keeps using the concrete *repo.UserRepo via embedding.
type userStatusRepo interface {
	GetByID(ctx context.Context, id string) (model.User, error)
	UpdateStatus(ctx context.Context, id string, status model.UserStatus) error
	CountByRole(ctx context.Context, role model.Role) (int, error)
}
```

- [ ] **Step 2: Add hub field + extended constructor**

Replace the `AuthService` struct + `NewAuthService` (lines 26–33) with:

```go
type AuthService struct {
	users    *repo.UserRepo
	sessions *repo.SessionRepo
	hub      *sse.Hub
}

func NewAuthService(users *repo.UserRepo, sessions *repo.SessionRepo, hub *sse.Hub) *AuthService {
	return &AuthService{users: users, sessions: sessions, hub: hub}
}
```

Add the import at the top of the file:

```go
	"github.com/blackforge/embookshelf/internal/sse"
```

- [ ] **Step 3: Add `ErrCannotTargetSelf` sentinel**

In the `var (...)` block at the top of `auth.go` (line 20), append:

```go
	ErrCannotTargetSelf = errors.New("cannot change your own approval status")
```

- [ ] **Step 4: Write the failing test for ApproveUser/DenyUser guards**

`internal/service/auth_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// fakeUserStatusRepo is the in-memory test seam. It implements only the
// methods userStatusRepo names; nothing else from UserRepo is needed.
type fakeUserStatusRepo struct {
	users map[string]model.User
}

func (f *fakeUserStatusRepo) GetByID(_ context.Context, id string) (model.User, error) {
	u, ok := f.users[id]
	if !ok {
		return model.User{}, repo.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserStatusRepo) UpdateStatus(_ context.Context, id string, status model.UserStatus) error {
	u, ok := f.users[id]
	if !ok {
		return repo.ErrNotFound
	}
	now := time.Now()
	u.Status = status
	u.StatusChangedAt = &now
	f.users[id] = u
	return nil
}

func (f *fakeUserStatusRepo) CountByRole(_ context.Context, role model.Role) (int, error) {
	n := 0
	for _, u := range f.users {
		if u.Role == role && u.Status == model.UserStatusActive {
			n++
		}
	}
	return n, nil
}

func newApproveTestSetup() (*fakeUserStatusRepo, model.User, model.User) {
	admin := model.User{ID: "admin-1", Email: "a@x", Role: model.RoleAdmin, Status: model.UserStatusActive}
	pending := model.User{ID: "u-2", Email: "p@x", Role: model.RoleUser, Status: model.UserStatusPending}
	return &fakeUserStatusRepo{users: map[string]model.User{
		admin.ID:   admin,
		pending.ID: pending,
	}}, admin, pending
}

func TestApproveUserFlipsPendingToActive(t *testing.T) {
	repo, admin, pending := newApproveTestSetup()

	if err := approveUser(context.Background(), repo, admin.ID, pending.ID); err != nil {
		t.Fatalf("approveUser: %v", err)
	}
	got := repo.users[pending.ID]
	if got.Status != model.UserStatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.StatusChangedAt == nil {
		t.Fatalf("status_changed_at not set")
	}
}

func TestApproveUserIsIdempotentOnActive(t *testing.T) {
	repo, admin, pending := newApproveTestSetup()
	pending.Status = model.UserStatusActive
	repo.users[pending.ID] = pending

	if err := approveUser(context.Background(), repo, admin.ID, pending.ID); err != nil {
		t.Fatalf("approveUser idempotent: %v", err)
	}
}

func TestDenyUserFlipsPendingToDenied(t *testing.T) {
	repo, admin, pending := newApproveTestSetup()
	if err := denyUser(context.Background(), repo, admin.ID, pending.ID); err != nil {
		t.Fatalf("denyUser: %v", err)
	}
	if repo.users[pending.ID].Status != model.UserStatusDenied {
		t.Fatalf("status = %q, want denied", repo.users[pending.ID].Status)
	}
}

func TestDenyUserRefusesSelf(t *testing.T) {
	repo, admin, _ := newApproveTestSetup()
	err := denyUser(context.Background(), repo, admin.ID, admin.ID)
	if !errors.Is(err, ErrCannotTargetSelf) {
		t.Fatalf("err = %v, want ErrCannotTargetSelf", err)
	}
}

func TestDenyUserRefusesLastAdmin(t *testing.T) {
	repo, admin, _ := newApproveTestSetup()
	other := model.User{ID: "admin-2", Email: "b@x", Role: model.RoleAdmin, Status: model.UserStatusActive}
	repo.users[other.ID] = other

	// Deny the second admin — should fail because the caller themselves is
	// also an admin and cannot leave the instance with no admins. Wait —
	// our rule is "cannot deny last remaining admin": with two admins,
	// denying one is allowed.
	if err := denyUser(context.Background(), repo, admin.ID, other.ID); err != nil {
		t.Fatalf("denying second admin should succeed: %v", err)
	}
	// Now only `admin` remains active. Try to deny them via an arbitrary
	// other-admin path — the guard should block.
	repo.users[admin.ID] = admin
	if err := denyUser(context.Background(), repo, "ghost", admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", err)
	}
}
```

- [ ] **Step 5: Run test to confirm failure**

Run: `go test ./internal/service/ -run TestApproveUser -v`
Expected: FAIL — `approveUser` and `denyUser` are not yet defined.

- [ ] **Step 6: Implement `approveUser`/`denyUser` (helpers) plus `ApproveUser`/`DenyUser` (methods)**

In `internal/service/auth.go`, append below `DeleteUser`:

```go
// approveUser/denyUser are pure helpers operating against any repo that
// satisfies userStatusRepo. They contain the guard logic and are unit
// tested in auth_test.go without a database. The exported method wrappers
// add SSE broadcast on success.

func approveUser(ctx context.Context, r userStatusRepo, callerID, targetID string) error {
	u, err := r.GetByID(ctx, targetID)
	if err != nil {
		return err
	}
	if u.Status == model.UserStatusActive {
		return nil // idempotent
	}
	return r.UpdateStatus(ctx, targetID, model.UserStatusActive)
}

func denyUser(ctx context.Context, r userStatusRepo, callerID, targetID string) error {
	if callerID == targetID {
		return ErrCannotTargetSelf
	}
	u, err := r.GetByID(ctx, targetID)
	if err != nil {
		return err
	}
	if u.Status == model.UserStatusDenied {
		return nil // idempotent
	}
	if u.Role == model.RoleAdmin && u.Status == model.UserStatusActive {
		n, err := r.CountByRole(ctx, model.RoleAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	return r.UpdateStatus(ctx, targetID, model.UserStatusDenied)
}

// ApproveUser flips a pending or denied user back to active. Idempotent
// for already-active users. Broadcasts a settings.users.updated SSE event
// so open admin tabs refresh.
func (s *AuthService) ApproveUser(ctx context.Context, callerID, targetID string) (model.User, error) {
	if err := approveUser(ctx, s.users, callerID, targetID); err != nil {
		return model.User{}, err
	}
	u, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return model.User{}, err
	}
	s.broadcastUsersUpdate()
	return u, nil
}

// DenyUser flips a pending user to denied. Idempotent. Refuses to deny
// the caller's own row or the last remaining admin.
func (s *AuthService) DenyUser(ctx context.Context, callerID, targetID string) (model.User, error) {
	if err := denyUser(ctx, s.users, callerID, targetID); err != nil {
		return model.User{}, err
	}
	u, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return model.User{}, err
	}
	s.broadcastUsersUpdate()
	return u, nil
}

func (s *AuthService) broadcastUsersUpdate() {
	if s.hub == nil {
		return
	}
	s.hub.Broadcast(sse.Event{Name: "users.updated", Data: "{}"})
}
```

- [ ] **Step 7: Run tests to confirm pass**

Run: `go test ./internal/service/ -run TestApproveUser -v && go test ./internal/service/ -run TestDenyUser -v`
Expected: PASS for all five test functions.

- [ ] **Step 8: Wire the hub into main.go**

In `cmd/embookshelf/main.go`, find the `service.NewAuthService` call (line 113):

```go
	authSvc := service.NewAuthService(userRepo, sessionRepo)
```

Replace with:

```go
	authSvc := service.NewAuthService(userRepo, sessionRepo, hub)
```

- [ ] **Step 9: Build the whole binary**

Run: `make build`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/service/auth.go internal/service/auth_test.go cmd/embookshelf/main.go
git commit -m "feat(auth): ApproveUser and DenyUser with last-admin and self guards"
```

---

## Task 7: Handlers — DTO, callback redirect, approve/deny endpoints

**Files:**
- Modify: `internal/handler/auth.go`
- Modify: `internal/handler/oidc.go`
- Create: `internal/handler/oidc_test.go`
- Modify: `internal/handler/users.go`
- Modify: `internal/handler/router.go`

- [ ] **Step 1: Extend `userDTO`**

In `internal/handler/auth.go`, replace the `userDTO` struct (lines 17–26) with:

```go
type userDTO struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	Status          string `json:"status"`
	StatusChangedAt string `json:"statusChangedAt,omitempty"`
	Display         string `json:"display"`
	Initials        string `json:"initials"`
	CreatedAt       string `json:"createdAt"`
	LastSeenAt      string `json:"lastSeenAt,omitempty"`
}
```

Update `toUserDTO` (lines 28–42) to map the new fields:

```go
func toUserDTO(u model.User) userDTO {
	d := userDTO{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		Role:      string(u.Role),
		Status:    string(u.Status),
		Display:   u.Display(),
		Initials:  u.Initials(),
		CreatedAt: u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if d.Status == "" {
		// Defensive default — older rows or test fakes may leave Status zero.
		d.Status = string(model.UserStatusActive)
	}
	if u.StatusChangedAt != nil {
		d.StatusChangedAt = u.StatusChangedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if u.LastSeenAt != nil {
		d.LastSeenAt = u.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return d
}
```

- [ ] **Step 2: Write failing test for `oidcErrorCode`**

`internal/handler/oidc_test.go`:

```go
package handler

import (
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/service"
)

func TestOIDCErrorCodeMaps(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{service.ErrOIDCStateMismatch, "stateMismatch"},
		{service.ErrOIDCLoginNotAllowed, "userNotProvisioned"},
		{service.ErrOIDCDisabled, "disabled"},
		{service.ErrOIDCNotConfigured, "notConfigured"},
		{service.ErrOIDCUnknownProvider, "notConfigured"},
		{service.ErrOIDCPendingApproval, "pendingApproval"},
		{errors.New("anything else"), "unknown"},
	}
	for _, tc := range cases {
		if got := oidcErrorCode(tc.err); got != tc.want {
			t.Errorf("oidcErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
```

- [ ] **Step 3: Run test to confirm it fails**

Run: `go test ./internal/handler/ -run TestOIDCErrorCodeMaps -v`
Expected: FAIL — `oidcErrorCode` does not yet emit `pendingApproval`.

- [ ] **Step 4: Update `oidcErrorCode` and the callback redirect**

In `internal/handler/oidc.go`, update `oidcErrorCode` (lines 112–127) to include the new sentinel:

```go
func oidcErrorCode(err error) string {
	switch {
	case errors.Is(err, service.ErrOIDCStateMismatch):
		return "stateMismatch"
	case errors.Is(err, service.ErrOIDCLoginNotAllowed):
		return "userNotProvisioned"
	case errors.Is(err, service.ErrOIDCDisabled):
		return "disabled"
	case errors.Is(err, service.ErrOIDCNotConfigured):
		return "notConfigured"
	case errors.Is(err, service.ErrOIDCUnknownProvider):
		return "notConfigured"
	case errors.Is(err, service.ErrOIDCPendingApproval):
		return "pendingApproval"
	default:
		return "unknown"
	}
}
```

Update `OIDCCallback` (lines 54–81). Replace the `sess, _, err := h.oidc.Exchange(...)` block (lines 74–80) with:

```go
	sess, _, err := h.oidc.Exchange(c.Request.Context(), code, state, c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, service.ErrOIDCPendingApproval) {
			c.Redirect(http.StatusFound, "/login/pending")
			return
		}
		c.Redirect(http.StatusFound, "/login?oidcError="+oidcErrorCode(err))
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.Redirect(http.StatusFound, "/")
```

- [ ] **Step 5: Run handler tests**

Run: `go test ./internal/handler/ -run TestOIDCErrorCodeMaps -v`
Expected: PASS.

- [ ] **Step 6: Add approve/deny handlers**

In `internal/handler/users.go`, append below `SettingsUsersDelete`:

```go
// callerID returns the authenticated user's ID, or "" when no session is
// attached. The admin guard upstream guarantees a session exists, but
// returning "" instead of panicking keeps this defensive.
func callerID(c *gin.Context) string {
	u, ok := auth.UserFromContext(c)
	if !ok || u == nil {
		return ""
	}
	return u.ID
}

// SettingsUsersApprove flips a pending or denied user to active.
func (h *Handler) SettingsUsersApprove(c *gin.Context) {
	u, err := h.auth.ApproveUser(c.Request.Context(), callerID(c), c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"user": toUserDTO(u)})
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "user not found")
	default:
		writeServerError(c, "settings users approve", err)
	}
}

// SettingsUsersDeny flips a pending user to denied. Refuses to deny the
// caller themselves or the last remaining admin.
func (h *Handler) SettingsUsersDeny(c *gin.Context) {
	u, err := h.auth.DenyUser(c.Request.Context(), callerID(c), c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"user": toUserDTO(u)})
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "user not found")
	case errors.Is(err, service.ErrCannotTargetSelf):
		writeError(c, http.StatusBadRequest, service.ErrCannotTargetSelf.Error())
	case errors.Is(err, service.ErrLastAdmin):
		writeError(c, http.StatusConflict, service.ErrLastAdmin.Error())
	default:
		writeServerError(c, "settings users deny", err)
	}
}
```

Add the import at the top of the file:

```go
	"github.com/blackforge/embookshelf/internal/auth"
```

- [ ] **Step 7: Wire the new routes**

In `internal/handler/router.go`, find the existing user routes (lines 149–152) and insert two new lines below `admin.PATCH("/users/:id/role", h.SettingsUsersUpdateRole)`:

```go
				admin.POST("/users/:id/approve", h.SettingsUsersApprove)
				admin.POST("/users/:id/deny", h.SettingsUsersDeny)
```

- [ ] **Step 8: Build and run all tests**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/handler/auth.go internal/handler/oidc.go internal/handler/oidc_test.go internal/handler/users.go internal/handler/router.go
git commit -m "feat(handler): /login/pending redirect and approve/deny endpoints"
```

---

## Task 8: Frontend API + types

**Files:**
- Modify: `ui/src/api/auth.ts`
- Modify: `ui/src/api/oidc.ts`
- Modify: `ui/src/api/settings.ts`
- Modify: `ui/src/api/realtime.ts`

- [ ] **Step 1: Extend `AuthUser` with status fields**

In `ui/src/api/auth.ts`, replace the `AuthUser` type (lines 5–14) with:

```ts
export type UserStatus = "active" | "pending" | "denied"

// Mirrors internal/handler/auth.go userDTO.
export type AuthUser = {
  id: string
  email: string
  name: string
  role: "admin" | "user"
  status: UserStatus
  statusChangedAt?: string
  display: string
  initials: string
  createdAt: string
  lastSeenAt?: string
}
```

- [ ] **Step 2: Extend `OidcAutoProvision`**

In `ui/src/api/oidc.ts`, replace the `OidcAutoProvision` type (lines 30–34) with:

```ts
export type OidcAutoProvision = {
  enableAutoProvisioning: boolean
  allowLocalAccountLinking: boolean
  defaultRole: "admin" | "user"
  requireAdminApproval: boolean
}
```

- [ ] **Step 3: Add approve/deny API helpers**

In `ui/src/api/settings.ts`, append below `deleteSettingsUser` (after line 303):

```ts
export async function approveSettingsUser(id: string): Promise<AuthUser> {
  const { user } = await api<{ user: AuthUser }>(
    `/api/v1/settings/users/${id}/approve`,
    { method: "POST" }
  )
  return user
}

export async function denySettingsUser(id: string): Promise<AuthUser> {
  const { user } = await api<{ user: AuthUser }>(
    `/api/v1/settings/users/${id}/deny`,
    { method: "POST" }
  )
  return user
}
```

- [ ] **Step 4: Wire the SSE event**

In `ui/src/api/realtime.ts`, update line 9 to include the new event name:

```ts
type RealtimeEvent = "bookdrop.updated" | "bookdrop.cleared" | "users.updated"
```

Add the import at the top of the file:

```ts
import { settingsUsersQueryKey } from "./settings"
```

Extend the `handlers` map (lines 24–35) with:

```ts
      "users.updated": () => {
        queryClient.invalidateQueries({ queryKey: settingsUsersQueryKey })
      },
```

- [ ] **Step 5: Typecheck**

Run: `make ui-typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/src/api/auth.ts ui/src/api/oidc.ts ui/src/api/settings.ts ui/src/api/realtime.ts
git commit -m "feat(ui-api): user status types and approve/deny endpoints"
```

---

## Task 9: `/login/pending` route

**Files:**
- Create: `ui/src/routes/login.pending.tsx`

- [ ] **Step 1: Look at the existing login route for styling cues**

Run: `head -80 ui/src/routes/login.tsx`
Expected: short component using project tokens (`t-h2`, `Card`, etc.). Keep the new route visually consistent.

- [ ] **Step 2: Create the route file**

`ui/src/routes/login.pending.tsx`:

```tsx
import { createFileRoute, Link } from "@tanstack/react-router"

import { Card } from "@/components/ui/Card"

export const Route = createFileRoute("/login/pending")({
  component: PendingApproval,
})

function PendingApproval() {
  return (
    <main
      style={{
        minHeight: "100vh",
        display: "grid",
        placeItems: "center",
        padding: 24,
      }}
    >
      <Card style={{ maxWidth: 460, width: "100%" }}>
        <h1 className="t-h2" style={{ marginBottom: 12 }}>
          Pending approval
        </h1>
        <p className="t-body" style={{ marginBottom: 12 }}>
          Your account has been created and is awaiting approval from an
          administrator. You will be able to sign in once it’s reviewed.
        </p>
        <p className="t-small" style={{ marginBottom: 18, fontStyle: "italic" }}>
          You can close this tab — there’s nothing else to do here.
        </p>
        <Link to="/login" className="t-link">
          Back to login
        </Link>
      </Card>
    </main>
  )
}
```

(If `Card` lives elsewhere, mirror the import path used in `login.tsx`. Verify by inspecting `ui/src/routes/login.tsx`.)

- [ ] **Step 3: Typecheck**

Run: `make ui-typecheck`
Expected: PASS.

- [ ] **Step 4: Manual smoke**

Run `make up` in one terminal. Visit `http://localhost:5173/login/pending`.
Expected: card with the heading "Pending approval" renders; "Back to login" link returns to `/login`.

- [ ] **Step 5: Commit**

```bash
git add ui/src/routes/login.pending.tsx
git commit -m "feat(ui): /login/pending landing page for OIDC users awaiting approval"
```

---

## Task 10: OIDC settings UI — `RequireAdminApproval` checkbox

**Files:**
- Modify: `ui/src/routes/_app.settings.tsx`

- [ ] **Step 1: Locate the auto-provisioning panel**

Open `ui/src/routes/_app.settings.tsx` around line 1495 (`<h3>Auto provisioning</h3>` block).

- [ ] **Step 2: Insert the new switch row**

Inside the `<Card>`'s flex column (right after the "Link by email" row that ends at line 1543, i.e. just before the `<Field label="Default role for new users">`), insert:

```tsx
          <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
            <div className="grow">
              <div className="t-item-title">Require admin approval</div>
              <div className="t-item-sub">
                New SSO users are created in a pending state and cannot sign in
                until an admin approves them in Users &amp; roles. Disabling
                this later does not auto-promote already-pending users.
              </div>
            </div>
            <Switch
              checked={draft.autoProvision.requireAdminApproval}
              disabled={!draft.autoProvision.enableAutoProvisioning}
              onCheckedChange={(v) =>
                setDraft({
                  ...draft,
                  autoProvision: {
                    ...draft.autoProvision,
                    requireAdminApproval: v,
                  },
                })
              }
            />
          </div>
```

- [ ] **Step 3: Typecheck + lint**

Run: `make ui-typecheck && make ui-lint`
Expected: PASS.

- [ ] **Step 4: Manual smoke**

With `make up` running and signed in as admin, navigate to `/settings → OIDC / SSO`. Toggle "Auto-create users on first login" off; the new "Require admin approval" switch is disabled. Toggle it on; the new switch becomes enabled. Save and reload — the value persists.

- [ ] **Step 5: Commit**

```bash
git add ui/src/routes/_app.settings.tsx
git commit -m "feat(ui): RequireAdminApproval toggle in OIDC auto-provisioning panel"
```

---

## Task 11: Users panel — pending pill, approve/deny actions, badge, sort

**Files:**
- Modify: `ui/src/routes/_app.settings.tsx`

- [ ] **Step 1: Import the new mutations + types**

In `ui/src/routes/_app.settings.tsx`, find the existing import for `fetchSettingsUsers` (line 31) and extend it:

```tsx
import {
  // existing imports...
  approveSettingsUser,
  denySettingsUser,
  fetchSettingsUsers,
  // ...
  settingsUsersQueryKey,
} from "@/api/settings"
```

(Insert `approveSettingsUser` and `denySettingsUser` next to `fetchSettingsUsers` in the existing import block.)

- [ ] **Step 2: Sort and partition the users list**

Inside `UsersPanel` (around line 1148), just after the `users` query is declared, add a memoised sorter:

```tsx
  const sortedUsers = useMemo(() => {
    const all = users.data ?? []
    const rank = (s: AuthUser["status"]) =>
      s === "pending" ? 0 : s === "active" ? 1 : 2
    return [...all].sort((a, b) => {
      const r = rank(a.status) - rank(b.status)
      if (r !== 0) return r
      if (a.status === "pending") {
        // Oldest pending first.
        return (
          new Date(a.statusChangedAt ?? a.createdAt).getTime() -
          new Date(b.statusChangedAt ?? b.createdAt).getTime()
        )
      }
      return a.email.localeCompare(b.email)
    })
  }, [users.data])

  const pendingCount = (users.data ?? []).filter(
    (u) => u.status === "pending"
  ).length
```

(Verify `useMemo` is in the React import at the top of the file; add it if missing.)

- [ ] **Step 3: Add approve/deny mutations**

Below the existing `deleteMut` (around line 1197), add:

```tsx
  const approveMut = useMutation({
    mutationFn: (id: string) => approveSettingsUser(id),
    onSuccess: () => {
      invalidate()
      toast.success("User approved.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const denyMut = useMutation({
    mutationFn: (id: string) => denySettingsUser(id),
    onSuccess: () => {
      invalidate()
      toast.success("User denied.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })
```

- [ ] **Step 4: Render the status pill + action buttons inside the user row**

Replace the iteration on `(users.data ?? []).map((u) => { ... })` (around line 1298) so it iterates over `sortedUsers` and renders the pill + new buttons. Inside each row, just before the existing `<Avatar>` block, add:

```tsx
              {u.status !== "active" && (
                <span
                  className="t-pill"
                  data-status={u.status}
                  style={{
                    padding: "2px 8px",
                    borderRadius: 999,
                    background:
                      u.status === "pending"
                        ? "var(--color-amber-bg, #fff5cc)"
                        : "var(--color-paper-2)",
                    color:
                      u.status === "pending"
                        ? "var(--color-amber-ink, #8a5b00)"
                        : "var(--color-ink-soft)",
                    fontSize: 11,
                    fontWeight: 500,
                  }}
                >
                  {u.status === "pending" ? "Pending" : "Denied"}
                </span>
              )}
```

After the `<Select>` for role and before the existing delete button, render the two new buttons conditionally:

```tsx
              {u.status === "pending" && (
                <>
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => approveMut.mutate(u.id)}
                    disabled={approveMut.isPending}
                  >
                    Approve
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => denyMut.mutate(u.id)}
                    disabled={denyMut.isPending || isMe}
                  >
                    Deny
                  </Button>
                </>
              )}
              {u.status === "denied" && (
                <Button
                  type="button"
                  size="sm"
                  onClick={() => approveMut.mutate(u.id)}
                  disabled={approveMut.isPending}
                >
                  Approve
                </Button>
              )}
```

For pending and denied users, hide the role `<Select>` and existing delete button by wrapping them with `{u.status === "active" && (...)}`. The simplest approach: check the existing JSX block lines 1327–1358 and wrap from `<Select>` through `</Button>` (delete) in `{u.status === "active" && (<>...</>)}`.

- [ ] **Step 5: Add the pending count badge to the settings nav**

Find the `tabs` array around line 96 — the entry `{ key: "users", label: "Users & roles", adminOnly: true }`. The nav rendering presumably loops over `tabs`. Locate the loop (search for `tabs.map`). In the rendered label cell, append a small numeric badge when the tab is `users` and the count is > 0.

The cleanest way: read `pendingCount` at the top of the parent component (`SettingsRoute` around line 100) by using the existing `users.data` query, or by calling `useQuery({ queryKey: settingsUsersQueryKey, queryFn: fetchSettingsUsers, enabled: isAdmin })` once at the parent level. (The `UsersPanel` already issues this query so the cache is warm.)

In the parent component, add:

```tsx
  const pendingUsers = useQuery({
    queryKey: settingsUsersQueryKey,
    queryFn: fetchSettingsUsers,
    enabled: isAdmin,
  })
  const pendingCount = (pendingUsers.data ?? []).filter(
    (u) => u.status === "pending"
  ).length
```

In the tab-rendering loop, when `tab.key === "users"` and `pendingCount > 0`, render a span next to the label:

```tsx
{tab.key === "users" && pendingCount > 0 && (
  <span
    className="t-badge"
    style={{
      marginLeft: 8,
      padding: "1px 6px",
      borderRadius: 999,
      background: "var(--color-amber-bg, #fff5cc)",
      color: "var(--color-amber-ink, #8a5b00)",
      fontSize: 10,
      fontWeight: 600,
    }}
  >
    {pendingCount}
  </span>
)}
```

- [ ] **Step 6: Typecheck + lint**

Run: `make ui-typecheck && make ui-lint`
Expected: PASS.

- [ ] **Step 7: Manual smoke (admin half)**

Backend running. Insert a fake pending user manually to verify the UI:
```
docker exec -i $(docker compose -f compose.dev.yml ps -q postgres-embookshelf) psql -U embookshelf -d embookshelf -c "INSERT INTO users (email, password_hash, name, role, oidc_issuer, oidc_subject, status, status_changed_at) VALUES ('pending@example.com', '', 'Pending Person', 'user', 'https://example.com', 'sub-1', 'pending', now());"
```
Visit `/settings → Users & roles` as admin. Expected: amber "1" badge on the Users tab; the row shows a "Pending" pill and Approve/Deny buttons. Approve → toast + the pill disappears + role/delete UI returns. Insert another pending user, then click Deny → pill flips to "Denied" + only Approve remains.

Cleanup: `docker exec ... psql ... -c "DELETE FROM users WHERE email LIKE 'pending%@example.com';"`

- [ ] **Step 8: Commit**

```bash
git add ui/src/routes/_app.settings.tsx
git commit -m "feat(ui): pending/denied status pills, approve/deny actions, badge"
```

---

## Task 12: E2E test — admin approves and denies pending users

**Files:**
- Create: `e2e/fixtures/sql/pending-user.sql`
- Create: `e2e/tests/admin-approval.spec.ts`

- [ ] **Step 1: Write the SQL fixture**

`e2e/fixtures/sql/pending-user.sql`:

```sql
-- Two deterministic pending users used by admin-approval.spec.ts. The
-- e2e harness loads this via psql before the spec runs.
INSERT INTO users (email, password_hash, name, role, oidc_issuer, oidc_subject, status, status_changed_at)
VALUES
  ('pending-approve@e2e.local', '', 'Pending Approve', 'user', 'https://e2e.local', 'e2e-approve', 'pending', now()),
  ('pending-deny@e2e.local',    '', 'Pending Deny',    'user', 'https://e2e.local', 'e2e-deny',    'pending', now())
ON CONFLICT (email) DO NOTHING;
```

- [ ] **Step 2: Write the spec**

`e2e/tests/admin-approval.spec.ts`:

```ts
import { execFileSync } from 'node:child_process'
import { resolve } from 'node:path'

import { test, expect } from '@playwright/test'

import { ADMIN_STATE_PATH } from '../fixtures/constants'

// Admin-only flow — reuse the cached admin session.
test.use({ storageState: ADMIN_STATE_PATH })

const FIXTURE = resolve(__dirname, '../fixtures/sql/pending-user.sql')
const SQL_DELETE_PENDING = `DELETE FROM users WHERE email LIKE '%@e2e.local';`

function psql(sql: string) {
  // The fixture container is named in compose.dev.yml as postgres-embookshelf.
  execFileSync('docker', [
    'exec', '-i',
    '$(docker compose -f compose.dev.yml ps -q postgres-embookshelf)',
    'psql', '-U', 'embookshelf', '-d', 'embookshelf',
    '-c', sql,
  ], { stdio: 'inherit', shell: true } as never)
}

function loadFixture(path: string) {
  const sql = require('node:fs').readFileSync(path, 'utf8')
  psql(sql)
}

test.beforeEach(() => {
  psql(SQL_DELETE_PENDING)
  loadFixture(FIXTURE)
})

test.afterEach(() => {
  psql(SQL_DELETE_PENDING)
})

test.describe('OIDC admin approval', () => {
  test('badge surfaces pending users and approve flips them to active', async ({ page }) => {
    await page.goto('/settings')
    await page.getByRole('button', { name: /Users & roles/ }).click()

    // Two pending users → badge reads "2".
    await expect(page.locator('[data-testid="users-tab-badge"], .t-badge').first()).toContainText('2')

    const row = page.locator('[data-row]').filter({ hasText: 'pending-approve@e2e.local' }).first()
    await expect(row.locator('.t-pill')).toHaveText('Pending')
    await row.getByRole('button', { name: 'Approve' }).click()

    await expect(row.locator('.t-pill')).toHaveCount(0)
    // Badge now reads "1" (one pending user remains).
    await expect(page.locator('.t-badge').first()).toContainText('1')
  })

  test('deny flips status to denied and keeps row durable', async ({ page }) => {
    await page.goto('/settings')
    await page.getByRole('button', { name: /Users & roles/ }).click()

    const row = page.locator('[data-row]').filter({ hasText: 'pending-deny@e2e.local' }).first()
    await row.getByRole('button', { name: 'Deny' }).click()

    await expect(row.locator('.t-pill')).toHaveText('Denied')
    await expect(row.getByRole('button', { name: 'Approve' })).toBeVisible()
    await expect(row.getByRole('button', { name: 'Deny' })).toHaveCount(0)
  })
})
```

(Adjust selectors if the Users panel rows use different `data-*` attributes — search for one match in `_app.settings.tsx` and use the most specific selector available. Add `data-row` to the row container in Task 11 if needed.)

- [ ] **Step 3: Add `data-row` and `data-testid` for stable selectors**

If selectors in step 2 don't match what the panel produces, go back into `ui/src/routes/_app.settings.tsx` and add to the row container created in Task 11:

```tsx
<div data-row="user" data-user-id={u.id} ...>
```

And to the badge in the tab navigation:

```tsx
<span data-testid="users-tab-badge" ...>
```

- [ ] **Step 4: Run the spec**

In one terminal: `make build && ./tmp/embookshelf` (with DB up + seeded). In another:
```
make e2e -- --grep "OIDC admin approval"
```
Expected: both tests pass.

- [ ] **Step 5: Commit**

```bash
git add e2e/fixtures/sql/pending-user.sql e2e/tests/admin-approval.spec.ts ui/src/routes/_app.settings.tsx
git commit -m "test(e2e): admin approval and denial flow"
```

---

## Task 13: Final integration check + lint pass

**Files:** none new; verification only.

- [ ] **Step 1: Run the full local CI**

Run: `make ci-local`
Expected: lint, typecheck, Go test, UI test all PASS in parallel.

- [ ] **Step 2: Manual full-stack smoke**

With `make up` running:

1. Sign in as admin. `/settings → OIDC / SSO`. Configure a real provider (or skip if not available); turn on "Auto-create users on first login" and "Require admin approval". Save.
2. Insert a fake pending OIDC user via psql (Task 11 step 7 snippet) — simulates an unknown user completing OIDC.
3. Verify `/settings → Users & roles` shows the badge and Approve/Deny actions.
4. Approve. The user's `status` flips to active.
5. Insert another pending user. Visit `/login/pending` directly to confirm the page renders.

- [ ] **Step 3: Commit nothing if no changes; otherwise commit fixes**

```bash
git status
```

If clean, the feature is done. If lint or typecheck flagged issues, fix them and commit:
```bash
git add -p
git commit -m "chore: address lint findings from full-stack smoke"
```

---

## Self-Review Checklist (post-write)

- **Spec coverage** — every section of the design doc maps to a task: configuration (Task 4, 8, 10), data model (Tasks 1–3), service flow (Task 5), approve/deny service (Task 6), API (Task 7), frontend client (Task 8), pending route (Task 9), settings UI (Task 10), Users panel UI (Task 11), edge cases (covered inline across Tasks 5–7 and the e2e spec in Task 12), testing (Task 6 unit; Task 7 unit; Task 12 e2e).
- **Placeholder scan** — no TBDs, no "implement later", no "similar to Task N". Each step has the literal code or command to run.
- **Type consistency** — `UserStatus`, `model.UserStatusActive/Pending/Denied`, `RequireAdminApproval`, `ErrOIDCPendingApproval`, `ErrCannotTargetSelf`, `ApproveUser`/`DenyUser`, `approveSettingsUser`/`denySettingsUser`, `users.updated` event name, `/login/pending` route, and `/api/v1/settings/users/:id/{approve,deny}` paths are used consistently across all tasks.
- **Spec re-read** — first-user-becomes-admin shortcut is preserved (Task 5 step 3); toggle-flipped-off behavior leaves pending rows untouched (covered by helper text in Task 10 step 2 and by service logic that only writes `pending` on path 3); denial is durable (Task 5 step 2 — denied users are rejected by path 1 immediately).
