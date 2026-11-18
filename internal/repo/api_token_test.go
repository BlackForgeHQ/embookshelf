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

func TestAPITokenRepo(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	tokens := repo.NewAPITokenRepo(d)
	ctx := context.Background()

	alice, err := users.Create(ctx, "alice@example.com", "Alice", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := users.Create(ctx, "bob@example.com", "Bob", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	expiry := now.Add(time.Hour)
	aliceHash := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	aliceTok, err := tokens.Create(ctx, alice.ID, "laptop", aliceHash, "abcd1234", &expiry)
	if err != nil {
		t.Fatalf("create alice token: %v", err)
	}
	bobHash := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	bobTok, err := tokens.Create(ctx, bob.ID, "ci", bobHash, "zzzz9999", nil)
	if err != nil {
		t.Fatalf("create bob token: %v", err)
	}

	t.Run("create round-trips every column", func(t *testing.T) {
		if aliceTok.ID == "" || aliceTok.UserID != alice.ID || aliceTok.Name != "laptop" || aliceTok.Prefix != "abcd1234" {
			t.Fatalf("row = %+v", aliceTok)
		}
		if aliceTok.ExpiresAt == nil || !aliceTok.ExpiresAt.Equal(expiry) {
			t.Fatalf("expires_at = %v, want %v", aliceTok.ExpiresAt, expiry)
		}
		if aliceTok.LastUsedAt != nil {
			t.Fatalf("a fresh token has last_used_at %v", aliceTok.LastUsedAt)
		}
		if bobTok.ExpiresAt != nil {
			t.Fatalf("nil expiry stored as %v", bobTok.ExpiresAt)
		}
	})

	t.Run("hash is unique", func(t *testing.T) {
		if _, err := tokens.Create(ctx, bob.ID, "dupe", aliceHash, "abcd1234", nil); err == nil {
			t.Fatal("a second row with the same hash was accepted")
		}
	})

	t.Run("lookup honours expiry", func(t *testing.T) {
		got, err := tokens.GetActiveByHash(ctx, aliceHash, now)
		if err != nil || got.ID != aliceTok.ID {
			t.Fatalf("live lookup = %+v, %v", got, err)
		}
		if _, err := tokens.GetActiveByHash(ctx, aliceHash, expiry.Add(time.Second)); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("expired lookup err = %v, want ErrNotFound", err)
		}
		if _, err := tokens.GetActiveByHash(ctx, bobHash, now.Add(100*365*24*time.Hour)); err != nil {
			t.Fatalf("never-expiring token refused: %v", err)
		}
		if _, err := tokens.GetActiveByHash(ctx, []byte("nope"), now); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("unknown hash err = %v, want ErrNotFound", err)
		}
	})

	t.Run("touch is throttled to once a minute", func(t *testing.T) {
		if err := tokens.Touch(ctx, aliceTok.ID, now); err != nil {
			t.Fatal(err)
		}
		if err := tokens.Touch(ctx, aliceTok.ID, now.Add(30*time.Second)); err != nil {
			t.Fatal(err)
		}
		got, _ := tokens.GetActiveByHash(ctx, aliceHash, now)
		if got.LastUsedAt == nil || !got.LastUsedAt.Equal(now) {
			t.Fatalf("last_used_at = %v, want the first touch %v", got.LastUsedAt, now)
		}
		later := now.Add(2 * time.Minute)
		if err := tokens.Touch(ctx, aliceTok.ID, later); err != nil {
			t.Fatal(err)
		}
		got, _ = tokens.GetActiveByHash(ctx, aliceHash, now)
		if got.LastUsedAt == nil || !got.LastUsedAt.Equal(later) {
			t.Fatalf("last_used_at = %v, want %v", got.LastUsedAt, later)
		}
	})

	t.Run("list for user sees only their own", func(t *testing.T) {
		got, err := tokens.ListForUser(ctx, alice.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != aliceTok.ID {
			t.Fatalf("alice's list = %+v", got)
		}
	})

	t.Run("list all carries the owner", func(t *testing.T) {
		got, err := tokens.ListAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("ListAll returned %d rows, want 2", len(got))
		}
		owners := map[string]string{}
		for _, r := range got {
			owners[r.ID] = r.OwnerEmail + "|" + r.OwnerName
		}
		if owners[aliceTok.ID] != "alice@example.com|Alice" || owners[bobTok.ID] != "bob@example.com|Bob" {
			t.Fatalf("owners = %v", owners)
		}
	})

	t.Run("delete for user refuses another user's token", func(t *testing.T) {
		if err := tokens.DeleteForUser(ctx, alice.ID, bobTok.ID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if err := tokens.DeleteForUser(ctx, alice.ID, aliceTok.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tokens.GetActiveByHash(ctx, aliceHash, now); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("revoked token still resolves")
		}
	})

	t.Run("admin delete and user cascade", func(t *testing.T) {
		if err := tokens.Delete(ctx, bobTok.ID); err != nil {
			t.Fatal(err)
		}
		if err := tokens.Delete(ctx, bobTok.ID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("second delete err = %v, want ErrNotFound", err)
		}

		h := []byte("cccccccccccccccccccccccccccccccc")
		if _, err := tokens.Create(ctx, bob.ID, "again", h, "cccccccc", nil); err != nil {
			t.Fatal(err)
		}
		if err := users.Delete(ctx, bob.ID); err != nil {
			t.Fatalf("delete bob: %v", err)
		}
		if _, err := tokens.GetActiveByHash(ctx, h, now); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("a deleted user's token still resolves")
		}
	})
}

func TestMCPSettingDefaultsOff(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewAppSettingsRepo(d, testCipher(t))
	ctx := context.Background()

	cfg, err := r.GetMCP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("MCP is on with no row")
	}
	if err := r.SetMCP(ctx, repo.MCPConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = r.GetMCP(ctx); !cfg.Enabled {
		t.Fatal("enabled row read back as off")
	}
}
