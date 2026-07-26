// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// user.go is the second file ADR-0023 names as fragile, and for a concrete
// reason: commit 909f6bf fixed swapped positional parameters in five of
// these update methods. The bug shape is that `$1`/`$2` bind by label
// while the Go argument slice binds by order, so a query can be wrong
// while its arguments look right.
//
// TouchLastSeen still deliberately numbers `$2` before `$1` to match its
// (id, at) argument order — correct, but a trap for anyone "tidying" it.
// Every production caller discards its error (`_ = s.users.TouchLastSeen`),
// so a break there has neither a return value nor a log to surface it.
// These tests are the only thing that would catch it.

func makeUser(t *testing.T, r *repo.UserRepo, email string) model.User {
	t.Helper()
	u, err := r.Create(context.Background(), email, "Test User", "hashed", model.RoleUser)
	if err != nil {
		t.Fatalf("Create(%s): %v", email, err)
	}
	return u
}

func TestUserRepo_CreateReadRoundTrip(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewUserRepo(d)
	ctx := context.Background()

	created := makeUser(t, r, "round@example.com")
	if created.ID == "" {
		t.Fatal("Create returned an empty id")
	}

	// Each field carries a distinct value, so a userCols/scanUser
	// crossing surfaces as a mismatch rather than passing silently.
	if created.Email != "round@example.com" {
		t.Errorf("Email = %q", created.Email)
	}
	if created.Name != "Test User" {
		t.Errorf("Name = %q, want Test User (crossed with another text column?)", created.Name)
	}
	if created.PasswordHash != "hashed" {
		t.Errorf("PasswordHash = %q, want hashed", created.PasswordHash)
	}
	if created.Role != model.RoleUser {
		t.Errorf("Role = %q, want user", created.Role)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero — timestamp column crossed or not scanned")
	}

	byID, err := r.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.Email != created.Email || byID.Name != created.Name || byID.Role != created.Role {
		t.Errorf("GetByID round-trip mismatch: %+v vs %+v", byID, created)
	}

	byEmail, err := r.GetByEmail(ctx, "ROUND@example.com")
	if err != nil {
		t.Fatalf("GetByEmail (case-insensitive): %v", err)
	}
	if byEmail.ID != created.ID {
		t.Errorf("GetByEmail returned %q, want %q", byEmail.ID, created.ID)
	}
}

// The method the ADR calls a trap: its Postgres text numbers $2 before $1
// so the (id, at) argument order works. Reversing either half silently
// writes the id into last_seen_at, or the timestamp into the WHERE.
func TestUserRepo_TouchLastSeenWritesTheTimestampNotTheID(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewUserRepo(d)
	ctx := context.Background()

	u := makeUser(t, r, "touch@example.com")
	if u.LastSeenAt != nil {
		t.Fatalf("fresh user should have no last_seen_at, got %v", u.LastSeenAt)
	}

	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := r.TouchLastSeen(ctx, u.ID, at); err != nil {
		t.Fatalf("TouchLastSeen: %v", err)
	}

	got, err := r.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.LastSeenAt == nil {
		t.Fatal("last_seen_at still NULL — the timestamp did not land")
	}
	if !got.LastSeenAt.UTC().Equal(at) {
		t.Errorf("last_seen_at = %v, want %v", got.LastSeenAt.UTC(), at)
	}
	// The row must still be findable: a swapped WHERE would have matched
	// nothing, or worse, matched on a timestamp.
	if got.ID != u.ID || got.Email != u.Email {
		t.Errorf("TouchLastSeen altered the wrong row: %+v", got)
	}
}

// The four methods 909f6bf fixed. Each writes one column and must leave
// its siblings untouched — a swapped parameter shows up as either a
// no-op (0 rows affected → ErrNotFound) or a value in the wrong column.
func TestUserRepo_UpdatersWriteOnlyTheirOwnColumn(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewUserRepo(d)
	ctx := context.Background()

	u := makeUser(t, r, "updates@example.com")

	if err := r.UpdatePassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	if err := r.UpdateName(ctx, u.ID, "Renamed"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	if err := r.UpdateRole(ctx, u.ID, model.RoleAdmin); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	if err := r.UpdateKindleEmail(ctx, u.ID, "reader@kindle.com"); err != nil {
		t.Fatalf("UpdateKindleEmail: %v", err)
	}

	got, err := r.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PasswordHash != "new-hash" {
		t.Errorf("PasswordHash = %q, want new-hash", got.PasswordHash)
	}
	if got.Name != "Renamed" {
		t.Errorf("Name = %q, want Renamed", got.Name)
	}
	if got.Role != model.RoleAdmin {
		t.Errorf("Role = %q, want admin", got.Role)
	}
	if got.KindleEmail != "reader@kindle.com" {
		t.Errorf("KindleEmail = %q, want reader@kindle.com", got.KindleEmail)
	}
	// Email is not touched by any of the four — if a swapped parameter
	// wrote into it, this catches it.
	if got.Email != "updates@example.com" {
		t.Errorf("Email = %q — an updater wrote into the wrong column", got.Email)
	}
}

// A miss must be reported, not silently succeed. This is what makes a
// swapped WHERE parameter detectable at all.
func TestUserRepo_UpdatersReportAMissingRow(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewUserRepo(d)
	ctx := context.Background()

	const ghost = "99999999-9999-4999-8999-999999999999"

	for name, call := range map[string]func() error{
		"UpdatePassword":    func() error { return r.UpdatePassword(ctx, ghost, "x") },
		"UpdateName":        func() error { return r.UpdateName(ctx, ghost, "x") },
		"UpdateRole":        func() error { return r.UpdateRole(ctx, ghost, model.RoleAdmin) },
		"UpdateKindleEmail": func() error { return r.UpdateKindleEmail(ctx, ghost, "x@kindle.com") },
	} {
		if err := call(); !errors.Is(err, repo.ErrNotFound) {
			t.Errorf("%s on a missing id returned %v, want ErrNotFound", name, err)
		}
	}
}

func TestUserRepo_CountAndCountByRole(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewUserRepo(d)
	ctx := context.Background()

	makeUser(t, r, "one@example.com")
	second := makeUser(t, r, "two@example.com")
	if err := r.UpdateRole(ctx, second.ID, model.RoleAdmin); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}

	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}

	admins, err := r.CountByRole(ctx, model.RoleAdmin)
	if err != nil {
		t.Fatalf("CountByRole: %v", err)
	}
	if admins != 1 {
		t.Errorf("CountByRole(admin) = %d, want 1", admins)
	}
}

func TestUserRepo_DeleteRemovesTheRow(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewUserRepo(d)
	ctx := context.Background()

	u := makeUser(t, r, "gone@example.com")
	if err := r.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.GetByID(ctx, u.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetByID after delete = %v, want ErrNotFound", err)
	}
}
