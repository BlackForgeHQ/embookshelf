package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestLibraryRepo_matrix runs a single end-to-end CRUD scenario against
// both SQLite and Postgres. The Postgres subtest is skipped when
// TEST_DATABASE_URL is unset (per repotest.NewWithDialect contract).
func TestLibraryRepo_matrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			r := repo.NewLibraryRepo(d)
			ctx := context.Background()

			// 1. Create
			lib, err := r.CreateLibrary(ctx, "My Library", "my-library", "/tmp/books")
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}
			if lib.ID == "" {
				t.Fatal("CreateLibrary returned empty ID")
			}
			if lib.Name != "My Library" || lib.Slug != "my-library" || lib.Path != "/tmp/books" {
				t.Fatalf("CreateLibrary fields = %+v, want name=My Library slug=my-library path=/tmp/books", lib)
			}

			// 2. Read back by id
			got, err := r.GetByID(ctx, lib.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.ID != lib.ID || got.Name != lib.Name {
				t.Fatalf("GetByID round-trip mismatch: got=%+v want id=%s name=%s", got, lib.ID, lib.Name)
			}

			// 3. List
			libs, err := r.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(libs) != 1 {
				t.Fatalf("List returned %d libs, want 1", len(libs))
			}

			// 4. Duplicate slug → ErrLibraryNameTaken
			_, err = r.CreateLibrary(ctx, "Other Name", "my-library", "/tmp/different")
			if !errors.Is(err, repo.ErrLibraryNameTaken) {
				t.Fatalf("dup slug: got err=%v, want ErrLibraryNameTaken", err)
			}

			// 5. Delete and confirm row is gone.
			// DeleteLibrary returns collected book IDs for cover-file cleanup
			// alongside the error; the library has no books so we expect an
			// empty slice.
			if _, err := r.DeleteLibrary(ctx, lib.ID); err != nil {
				t.Fatalf("DeleteLibrary: %v", err)
			}
			if _, err := r.GetByID(ctx, lib.ID); !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("post-delete GetByID: got err=%v, want ErrNotFound", err)
			}
		})
	}
}
