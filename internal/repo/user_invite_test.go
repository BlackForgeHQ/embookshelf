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

// The user_invites table used to keep its column list in two hand-kept
// SELECT lists with the Scan destinations stated separately again — the
// Column-order coupling hazard from CONTEXT.md. Three of the eight
// columns are TIMESTAMPTZ (created_at, expires_at, accepted_at) and two
// are id-shaped (invited_by, user_id), so swapping an adjacent pair in
// one list compiled, ran, and crossed every invite's issue time with its
// deadline: an auth-surface bug that reads as an expiry problem.
//
// Every field below carries a value distinct from every other field of
// its type, and the three instants sit hours apart rather than at now()
// three times.
//
// One honest limitation, stated rather than implied: both read paths
// filter on `accepted_at IS NULL`, so no repo method can ever return a
// row whose accepted_at or user_id is non-NULL. A crossing that puts
// either of those into a non-nullable destination fails loudly as a
// NULL-scan error, but their *values* can only be checked on the write
// side — which is what the direct SQL read after MarkAccepted does.

// assertInviteInstant reports a timestamp that landed outside its
// expected window, naming the field so a crossing is readable.
func assertInviteInstant(t *testing.T, where, field string, got, want time.Time, tol time.Duration) {
	t.Helper()
	if delta := got.Sub(want); delta > tol || delta < -tol {
		t.Errorf("%s: %s = %v, want within %v of %v (off by %v) — a timestamp column/scan-order crossing looks exactly like this",
			where, field, got, tol, want, delta)
	}
}

// TestUserInviteRepo_RoundTripPreservesEveryField exercises both SELECT
// lists — GetByHash's and ListPending's — plus MarkAccepted's SET list,
// with values no two of which can be confused.
func TestUserInviteRepo_RoundTripPreservesEveryField(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	users := repo.NewUserRepo(d)
	invites := repo.NewUserInviteRepo(d)

	admin, err := users.Create(ctx, "inviter@example.com", "Inviter", "hash", model.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// A second user id that is never the answer, so a crossed
	// invited_by/user_id has somewhere wrong to point.
	bystander, err := users.Create(ctx, "bystander@example.com", "Bystander", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create bystander: %v", err)
	}

	const (
		wantEmail = "round.trip.invitee@example.com"
		// Mixed case going in proves lower($2) is still applied.
		inputEmail = "Round.Trip.Invitee@Example.COM"
		// The role must be one of the two the CHECK allows; admin is
		// the one that is not the common default.
		wantRole   = model.RoleAdmin
		instantTol = 30 * time.Second
	)
	hash := []byte("round-trip-token-hash-0123456789")
	// Postgres stores timestamptz at microsecond precision, so truncate
	// before comparing for equality.
	expiresAt := time.Now().Add(1000 * time.Hour).UTC().Truncate(time.Microsecond)

	before := time.Now()
	if err := invites.Create(ctx, hash, inputEmail, wantRole, admin.ID, expiresAt); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// assertPending checks a row read back through either SELECT list.
	assertPending := func(where string, inv repo.UserInvite) {
		t.Helper()
		if string(inv.TokenHash) != string(hash) {
			t.Errorf("%s: TokenHash = %q, want %q", where, inv.TokenHash, hash)
		}
		if inv.Email != wantEmail {
			t.Errorf("%s: Email = %q, want %q", where, inv.Email, wantEmail)
		}
		if inv.Role != wantRole {
			t.Errorf("%s: Role = %q, want %q", where, inv.Role, wantRole)
		}
		if inv.InvitedBy != admin.ID {
			t.Errorf("%s: InvitedBy = %q, want %q — the two uuid columns crossed", where, inv.InvitedBy, admin.ID)
		}
		if inv.InvitedBy == bystander.ID {
			t.Errorf("%s: InvitedBy points at the bystander", where)
		}
		// created_at is the insert instant, expires_at is 1000h past
		// it. A crossing of that pair is off by six weeks.
		assertInviteInstant(t, where, "CreatedAt", inv.CreatedAt, before, instantTol)
		if !inv.ExpiresAt.Equal(expiresAt) {
			t.Errorf("%s: ExpiresAt = %v, want %v — the two timestamp columns crossed",
				where, inv.ExpiresAt.UTC(), expiresAt)
		}
		if !inv.ExpiresAt.After(inv.CreatedAt) {
			t.Errorf("%s: ExpiresAt (%v) is not after CreatedAt (%v) — the two timestamp columns crossed",
				where, inv.ExpiresAt, inv.CreatedAt)
		}
		if inv.AcceptedAt != nil {
			t.Errorf("%s: AcceptedAt = %v on a pending row, want nil", where, *inv.AcceptedAt)
		}
		if inv.UserID != nil {
			t.Errorf("%s: UserID = %q on a pending row, want nil", where, *inv.UserID)
		}
	}

	byHash, err := invites.GetByHash(ctx, hash, time.Now())
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	assertPending("GetByHash", byHash)

	pending, err := invites.ListPending(ctx, time.Now())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("ListPending returned %d rows, want 1", len(pending))
	}
	assertPending("ListPending", pending[0])

	// MarkAccepted is the only writer of accepted_at and user_id, and no
	// read path can return them, so its SET list is checked against the
	// row directly. acceptedAt is deliberately a future instant distinct
	// from both created_at and expires_at — the statement only requires
	// it to be before the deadline — so all three timestamps differ.
	invitee, err := users.Create(ctx, wantEmail, "Invitee", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	acceptedAt := time.Now().Add(500 * time.Hour).UTC().Truncate(time.Microsecond)
	if err := invites.MarkAccepted(ctx, hash, invitee.ID, acceptedAt); err != nil {
		t.Fatalf("MarkAccepted: %v", err)
	}

	var (
		gotCreated   time.Time
		gotExpires   time.Time
		gotAccepted  *time.Time
		gotUserID    *string
		gotInvitedBy string
	)
	if err := d.SQL.QueryRowContext(ctx, `
		SELECT created_at, expires_at, accepted_at, user_id, invited_by
		FROM user_invites WHERE token_hash = $1`, hash).
		Scan(&gotCreated, &gotExpires, &gotAccepted, &gotUserID, &gotInvitedBy); err != nil {
		t.Fatalf("read accepted row: %v", err)
	}
	if gotAccepted == nil {
		t.Fatal("accepted_at is NULL after MarkAccepted")
	}
	if !gotAccepted.Equal(acceptedAt) {
		t.Errorf("accepted_at = %v, want %v — MarkAccepted's SET list crossed its arguments",
			gotAccepted.UTC(), acceptedAt)
	}
	if gotUserID == nil || *gotUserID != invitee.ID {
		t.Errorf("user_id = %v, want %q — MarkAccepted's SET list crossed its arguments", gotUserID, invitee.ID)
	}
	if gotInvitedBy != admin.ID {
		t.Errorf("invited_by = %q, want %q — accepting rewrote the inviter", gotInvitedBy, admin.ID)
	}
	assertInviteInstant(t, "MarkAccepted", "created_at", gotCreated, before, instantTol)
	if !gotExpires.Equal(expiresAt) {
		t.Errorf("expires_at = %v, want %v — accepting moved the deadline", gotExpires.UTC(), expiresAt)
	}
}

func TestUserInviteRepo_crud(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	invites := repo.NewUserInviteRepo(d)
	ctx := context.Background()

	admin, err := users.Create(ctx, "admin@example.com", "Admin", "hash", model.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	hash := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	expires := time.Now().Add(7 * 24 * time.Hour)
	if err := invites.Create(ctx, hash, "newcomer@example.com", model.RoleUser, admin.ID, expires); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	t.Run("get by hash returns valid row", func(t *testing.T) {
		inv, err := invites.GetByHash(ctx, hash, time.Now())
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if inv.Email != "newcomer@example.com" {
			t.Fatalf("email = %q, want lowercase newcomer@example.com", inv.Email)
		}
		if inv.Role != model.RoleUser {
			t.Fatalf("role = %q, want user", inv.Role)
		}
		if inv.InvitedBy != admin.ID {
			t.Fatalf("invited_by = %q, want %q", inv.InvitedBy, admin.ID)
		}
	})

	t.Run("list pending includes the row", func(t *testing.T) {
		rows, err := invites.ListPending(ctx, time.Now())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("len = %d, want 1", len(rows))
		}
	})

	t.Run("mark accepted seals the row and second accept fails", func(t *testing.T) {
		newcomer, err := users.Create(ctx, "newcomer@example.com", "New", "hash", model.RoleUser)
		if err != nil {
			t.Fatalf("create newcomer: %v", err)
		}
		if err := invites.MarkAccepted(ctx, hash, newcomer.ID, time.Now()); err != nil {
			t.Fatalf("mark accepted: %v", err)
		}
		if err := invites.MarkAccepted(ctx, hash, newcomer.ID, time.Now()); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("second accept err = %v, want ErrNotFound", err)
		}
	})

	t.Run("revoke pending invite is idempotent", func(t *testing.T) {
		rev := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		if err := invites.Create(ctx, rev, "revoked@example.com", model.RoleUser, admin.ID, expires); err != nil {
			t.Fatalf("create revoke target: %v", err)
		}
		if err := invites.Revoke(ctx, rev); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if err := invites.Revoke(ctx, rev); err != nil {
			t.Fatalf("second revoke: %v", err)
		}
	})

	t.Run("expired invite is invisible to GetByHash", func(t *testing.T) {
		expHash := []byte("cccccccccccccccccccccccccccccccc")
		past := time.Now().Add(-time.Hour)
		if err := invites.Create(ctx, expHash, "stale@example.com", model.RoleUser, admin.ID, past); err != nil {
			t.Fatalf("create expired: %v", err)
		}
		if _, err := invites.GetByHash(ctx, expHash, time.Now()); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("expired GetByHash err = %v, want ErrNotFound", err)
		}
	})
}
