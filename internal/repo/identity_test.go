package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func TestIdentityRepo_matrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			ur := repo.NewUserRepo(d)
			ir := repo.NewIdentityRepo(d)
			ctx := context.Background()

			// Two users — alice has a password, bob will be OIDC-only
			// to exercise the lockout guard.
			alice, err := ur.Create(ctx, "alice@example.com", "Alice", "alice-hash", model.RoleUser)
			if err != nil {
				t.Fatalf("create alice: %v", err)
			}
			bob, err := ur.CreateOIDC(ctx, "bob@example.com", "Bob", model.RoleUser)
			if err != nil {
				t.Fatalf("create bob: %v", err)
			}

			// Insert a Google identity for alice.
			gIdent, err := ir.Insert(ctx, alice.ID, "google", "https://accounts.google.com", "alice-sub", alice.Email)
			if err != nil {
				t.Fatalf("insert google: %v", err)
			}
			if gIdent.UserID != alice.ID || gIdent.Provider != "google" {
				t.Fatalf("unexpected identity: %+v", gIdent)
			}

			// (issuer, subject) must look up to the same identity.
			got, err := ir.GetByIssuerSubject(ctx, "https://accounts.google.com", "alice-sub")
			if err != nil {
				t.Fatalf("get by pair: %v", err)
			}
			if got.ID != gIdent.ID {
				t.Fatalf("get returned %q, want %q", got.ID, gIdent.ID)
			}

			// Linking the same provider again with a new subject is
			// blocked by UNIQUE(user_id, provider).
			if _, err := ir.Insert(ctx, alice.ID, "google", "https://accounts.google.com", "alice-sub-2", alice.Email); err == nil {
				t.Fatalf("expected duplicate-provider rejection, got nil")
			}

			// (issuer, subject) belonging to alice cannot be linked to bob.
			if _, err := ir.Insert(ctx, bob.ID, "google", "https://accounts.google.com", "alice-sub", bob.Email); !errors.Is(err, repo.ErrIdentityForeignUser) {
				t.Fatalf("expected ErrIdentityForeignUser, got %v", err)
			}

			// GitHub is a different provider — alice can link both.
			if _, err := ir.Insert(ctx, alice.ID, "github", "https://github.com", "alice-gh", alice.Email); err != nil {
				t.Fatalf("insert github: %v", err)
			}
			if n, err := ir.CountByUser(ctx, alice.ID); err != nil || n != 2 {
				t.Fatalf("CountByUser alice = (%d, %v), want (2, nil)", n, err)
			}

			// alice has a password, so unlinking a single identity is
			// allowed even when it's her last one.
			deleted, err := ir.DeleteWithGuard(ctx, alice.ID, "github")
			if err != nil || !deleted {
				t.Fatalf("alice unlink github = (%v, %v), want (true, nil)", deleted, err)
			}
			deleted, err = ir.DeleteWithGuard(ctx, alice.ID, "google")
			if err != nil || !deleted {
				t.Fatalf("alice unlink google = (%v, %v), want (true, nil)", deleted, err)
			}

			// Bob — OIDC-provisioned, no password. Link Google then
			// try to unlink: guard must refuse.
			if _, err := ir.Insert(ctx, bob.ID, "google", "https://accounts.google.com", "bob-sub", bob.Email); err != nil {
				t.Fatalf("insert bob google: %v", err)
			}
			deleted, err = ir.DeleteWithGuard(ctx, bob.ID, "google")
			if err == nil || deleted {
				t.Fatalf("bob unlink last identity = (%v, %v), want (false, lockout)", deleted, err)
			}
			if !errors.Is(err, repo.ErrIdentityLockout) {
				t.Fatalf("unexpected error: %v", err)
			}

			// Add a second identity — first one becomes removable.
			if _, err := ir.Insert(ctx, bob.ID, "github", "https://github.com", "bob-gh", bob.Email); err != nil {
				t.Fatalf("insert bob github: %v", err)
			}
			deleted, err = ir.DeleteWithGuard(ctx, bob.ID, "google")
			if err != nil || !deleted {
				t.Fatalf("bob unlink with backup = (%v, %v), want (true, nil)", deleted, err)
			}

			// Unlinking again is a not-found, not a lockout.
			if _, err := ir.DeleteWithGuard(ctx, bob.ID, "google"); !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("repeat unlink = %v, want ErrNotFound", err)
			}

			// RelinkProvider replaces the existing slot atomically.
			ident, err := ir.RelinkProvider(ctx, bob.ID, "github", "https://github.com", "bob-gh-new", bob.Email)
			if err != nil {
				t.Fatalf("relink github: %v", err)
			}
			if ident.Subject != "bob-gh-new" {
				t.Fatalf("relink kept old subject %q", ident.Subject)
			}
			if n, err := ir.CountByUser(ctx, bob.ID); err != nil || n != 1 {
				t.Fatalf("CountByUser bob after relink = (%d, %v), want (1, nil)", n, err)
			}
		})
	}
}
