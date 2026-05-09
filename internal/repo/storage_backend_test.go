// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func TestStorageBackendRepo_matrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			r := repo.NewStorageBackendRepo(d)
			lr := repo.NewLibraryRepo(d)
			ctx := context.Background()

			// 1. Create + List
			cfg := map[string]any{"root": "/tmp/books"}
			b, err := r.Create(ctx, "local", cfg)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if b.ID == "" {
				t.Fatal("Create returned empty ID")
			}
			if b.Kind != "local" {
				t.Fatalf("Kind=%q want local", b.Kind)
			}
			if b.Config["root"] != "/tmp/books" {
				t.Fatalf("Config root=%v want /tmp/books", b.Config["root"])
			}
			if b.CreatedAt.IsZero() {
				t.Fatal("CreatedAt is zero")
			}

			list, err := r.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(list) != 1 {
				t.Fatalf("List len=%d want 1", len(list))
			}
			if list[0].ID != b.ID {
				t.Fatalf("List[0].ID=%q want %q", list[0].ID, b.ID)
			}

			// 2. Get found
			got, err := r.Get(ctx, b.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.ID != b.ID || got.Kind != b.Kind {
				t.Fatalf("Get round-trip mismatch: %+v", got)
			}

			// 3. Get not found
			_, err = r.Get(ctx, "00000000-0000-0000-0000-000000000000")
			if !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("Get missing: got %v, want ErrNotFound", err)
			}

			// 4. Delete success (no libraries reference it yet)
			if err := r.Delete(ctx, b.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			// Confirm it's gone
			if _, err := r.Get(ctx, b.ID); !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("post-delete Get: got %v, want ErrNotFound", err)
			}

			// 5. Delete-while-library-references-it → ErrStorageBackendInUse
			//
			// Create a fresh backend, wire up a library to it, then try to
			// delete the backend. The FK ON DELETE RESTRICT should fire.
			b2, err := r.Create(ctx, "local", map[string]any{"root": "/tmp/ref"})
			if err != nil {
				t.Fatalf("Create b2: %v", err)
			}
			// Create a library that points at b2.
			lib, err := lr.CreateLibrary(ctx, "Ref Library", "ref-library", "/tmp/ref", nil)
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}
			// Wire the library to b2 via backend_id.
			if err := lr.SetBackendID(ctx, lib.ID, b2.ID); err != nil {
				t.Fatalf("SetBackendID: %v", err)
			}
			// Now deleting b2 should fail.
			err = r.Delete(ctx, b2.ID)
			if !errors.Is(err, repo.ErrStorageBackendInUse) {
				t.Fatalf("Delete in-use backend: got %v, want ErrStorageBackendInUse", err)
			}
		})
	}
}
