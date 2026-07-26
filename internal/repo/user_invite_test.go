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
