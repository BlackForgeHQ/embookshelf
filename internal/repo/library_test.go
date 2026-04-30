package repo_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestLibraryRepo_backendFields verifies that the new storage_v2 columns
// (backend_id, root, org_mode) round-trip through the repo layer and that
// LibraryBackend joins through correctly.
func TestLibraryRepo_backendFields(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			lr := repo.NewLibraryRepo(d)
			sbr := repo.NewStorageBackendRepo(d)
			ctx := context.Background()

			// 1. Fresh library has nil BackendID and nil Root; OrgMode defaults to 'book_per_folder'.
			lib, err := lr.CreateLibrary(ctx, "Backend Test", "backend-test", "/tmp/bt", nil)
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}
			if lib.BackendID != nil {
				t.Fatalf("BackendID should be nil on fresh library, got %v", lib.BackendID)
			}
			// Root equals path for local libraries (plan spec: "root for local
			// libraries equals path").
			if lib.Root == nil || *lib.Root != "/tmp/bt" {
				t.Fatalf("Root should be /tmp/bt on fresh local library, got %v", lib.Root)
			}
			if lib.OrgMode != "book_per_folder" {
				t.Fatalf("OrgMode=%q want book_per_folder", lib.OrgMode)
			}

			// 2. LibraryBackend returns ErrNotFound when no backend is set.
			_, err = lr.LibraryBackend(ctx, lib.ID)
			if !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("LibraryBackend (no backend): got %v, want ErrNotFound", err)
			}

			// 3. Wire a backend and read the library back.
			backend, err := sbr.Create(ctx, "local", map[string]any{"root": "/data"})
			if err != nil {
				t.Fatalf("Create backend: %v", err)
			}
			if err := lr.SetBackendID(ctx, lib.ID, backend.ID); err != nil {
				t.Fatalf("SetBackendID: %v", err)
			}
			got, err := lr.GetByID(ctx, lib.ID)
			if err != nil {
				t.Fatalf("GetByID after SetBackendID: %v", err)
			}
			if got.BackendID == nil || *got.BackendID != backend.ID {
				t.Fatalf("BackendID=%v want %q", got.BackendID, backend.ID)
			}

			// 4. LibraryBackend now returns the joined backend row.
			sb, err := lr.LibraryBackend(ctx, lib.ID)
			if err != nil {
				t.Fatalf("LibraryBackend: %v", err)
			}
			if sb.ID != backend.ID {
				t.Fatalf("LibraryBackend ID=%q want %q", sb.ID, backend.ID)
			}
			if sb.Kind != "local" {
				t.Fatalf("LibraryBackend Kind=%q want local", sb.Kind)
			}
		})
	}
}

// TestLibraryRepo_setCoverHash verifies that SetCoverHash persists the sha256
// bytes and that GetBookByID returns them, and that ListBooksMissingCoverHash
// reflects the transition correctly.
func TestLibraryRepo_setCoverHash(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			r := repo.NewLibraryRepo(d)
			br := repo.NewBookRepo(d)
			ctx := context.Background()

			lib, err := r.CreateLibrary(ctx, "Cover Test", "cover-test", "/tmp/ct", nil)
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}

			// Create a book with has_cover=true so it appears in ListMissingCoverHash.
			book, err := br.Create(ctx, model.Book{
				LibraryID: lib.ID,
				Title:     "Hashed Cover Book",
				HasCover:  true,
				CoverMime: "image/jpeg",
			})
			if err != nil {
				t.Fatalf("Create book: %v", err)
			}
			if book.CoverHash != nil {
				t.Fatalf("fresh book should have nil CoverHash, got %x", book.CoverHash)
			}

			// ListMissingCoverHash should return our book.
			missing, err := br.ListMissingCoverHash(ctx, 100)
			if err != nil {
				t.Fatalf("ListMissingCoverHash: %v", err)
			}
			found := false
			for _, b := range missing {
				if b.ID == book.ID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("book not in ListMissingCoverHash result (got %d books)", len(missing))
			}

			// SetCoverHash and read back.
			hash := sha256.Sum256([]byte("fake cover bytes"))
			if err := br.SetCoverHash(ctx, book.ID, hash[:]); err != nil {
				t.Fatalf("SetCoverHash: %v", err)
			}

			got, err := br.GetByID(ctx, "", book.ID)
			if err != nil {
				t.Fatalf("GetByID after SetCoverHash: %v", err)
			}
			if !bytes.Equal(got.CoverHash, hash[:]) {
				t.Fatalf("CoverHash mismatch: got %x, want %x", got.CoverHash, hash[:])
			}

			// ListMissingCoverHash should no longer return our book.
			missing2, err := br.ListMissingCoverHash(ctx, 100)
			if err != nil {
				t.Fatalf("ListMissingCoverHash (after set): %v", err)
			}
			for _, b := range missing2 {
				if b.ID == book.ID {
					t.Fatal("book still in ListMissingCoverHash after SetCoverHash")
				}
			}
		})
	}
}

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
			lib, err := r.CreateLibrary(ctx, "My Library", "my-library", "/tmp/books", nil)
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
			_, err = r.CreateLibrary(ctx, "Other Name", "my-library", "/tmp/different", nil)
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
