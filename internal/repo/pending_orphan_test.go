package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestPendingOrphanRepo_RoundTrip exercises the basic insert / select-due
// / delete cycle on both dialects.
func TestPendingOrphanRepo_RoundTrip(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			lr := repo.NewLibraryRepo(d)
			pr := repo.NewPendingOrphanRepo(d)
			ctx := context.Background()

			lib, err := lr.CreateLibrary(ctx, "Orphan Test", "orphan-test", "/tmp/ot", nil)
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}

			now := time.Now().UTC()
			past := now.Add(-time.Hour)
			future := now.Add(time.Hour)
			bookID := "book-1"
			rows := []repo.PendingOrphanInsert{
				{LibraryID: lib.ID, Key: "Old/Folder/file.epub", EligibleAt: past, Reason: repo.ReasonOrphanRename, BookID: &bookID},
				{LibraryID: lib.ID, Key: "Old/Folder/cover.jpg", EligibleAt: past, Reason: repo.ReasonOrphanRename, BookID: &bookID},
				{LibraryID: lib.ID, Key: "Other/key.epub", EligibleAt: future, Reason: repo.ReasonOrphanRename, BookID: &bookID},
			}
			if err := pr.Insert(ctx, rows); err != nil {
				t.Fatalf("Insert: %v", err)
			}

			// Re-insert is a no-op (UNIQUE library_id+key + ON CONFLICT DO NOTHING).
			if err := pr.Insert(ctx, rows[:1]); err != nil {
				t.Fatalf("Insert (dup): %v", err)
			}

			due, err := pr.SelectDue(ctx, now, 10)
			if err != nil {
				t.Fatalf("SelectDue: %v", err)
			}
			if len(due) != 2 {
				t.Fatalf("SelectDue returned %d; want 2 (the two past-eligible)", len(due))
			}
			for _, po := range due {
				if po.LibraryID != lib.ID {
					t.Errorf("library_id=%q want %q", po.LibraryID, lib.ID)
				}
				if po.Reason != repo.ReasonOrphanRename {
					t.Errorf("reason=%q", po.Reason)
				}
				if po.BookID == nil || *po.BookID != bookID {
					t.Errorf("book_id=%v want %q", po.BookID, bookID)
				}
			}

			// Delete one; only the other due row remains.
			if err := pr.Delete(ctx, due[0].ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			due2, err := pr.SelectDue(ctx, now, 10)
			if err != nil {
				t.Fatalf("SelectDue after delete: %v", err)
			}
			if len(due2) != 1 {
				t.Fatalf("SelectDue post-delete=%d want 1", len(due2))
			}

			// Deleting a missing id is not an error.
			if err := pr.Delete(ctx, 999_999); err != nil {
				t.Errorf("Delete missing: %v", err)
			}
		})
	}
}
