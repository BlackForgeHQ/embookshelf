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

func TestPasswordResetTokenRepo_crud(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	tokens := repo.NewPasswordResetTokenRepo(d)
	ctx := context.Background()

	alice, err := users.Create(ctx, "alice@example.com", "Alice", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	hash := []byte("11111111111111111111111111111111")
	expires := time.Now().Add(time.Hour)
	if err := tokens.Create(ctx, hash, alice.ID, expires); err != nil {
		t.Fatalf("create token: %v", err)
	}

	t.Run("verify returns row when usable", func(t *testing.T) {
		row, err := tokens.Verify(ctx, hash, time.Now())
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if row.UserID != alice.ID {
			t.Fatalf("user_id = %q, want %q", row.UserID, alice.ID)
		}
	})

	t.Run("consume marks used and refuses replay", func(t *testing.T) {
		row, err := tokens.Consume(ctx, hash, time.Now())
		if err != nil {
			t.Fatalf("first consume: %v", err)
		}
		if row.UserID != alice.ID {
			t.Fatalf("user_id = %q, want %q", row.UserID, alice.ID)
		}
		if _, err := tokens.Consume(ctx, hash, time.Now()); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("second consume err = %v, want ErrNotFound", err)
		}
	})

	t.Run("expired token cannot be consumed", func(t *testing.T) {
		expiredHash := []byte("22222222222222222222222222222222")
		past := time.Now().Add(-time.Minute)
		if err := tokens.Create(ctx, expiredHash, alice.ID, past); err != nil {
			t.Fatalf("create expired: %v", err)
		}
		if _, err := tokens.Consume(ctx, expiredHash, time.Now()); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("expired consume err = %v, want ErrNotFound", err)
		}
	})

	t.Run("count recent tracks rate limit window", func(t *testing.T) {
		since := time.Now().Add(-time.Minute)
		n, err := tokens.CountRecentForUser(ctx, alice.ID, since)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n < 1 {
			t.Fatalf("count = %d, want >= 1", n)
		}
	})
}
