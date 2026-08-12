// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestPendingOrphanRepo_RoundTrip exercises the basic insert / select-due
// / delete cycle.
func TestPendingOrphanRepo_RoundTrip(t *testing.T) {
	d := repotest.New(t)
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
	// A real UUID: pending_orphans.book_id is uuid on Postgres.
	bookID := "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
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
}

// TestSelectDueRoundTripsDistinctFieldsPerRow exercises
// duePendingOrphanProjection directly: library_id, key and reason are
// three adjacent TEXT columns in the SELECT, and the EXISTS join that
// fills Referenced sits right after them. A crossed pair among the three
// would compile, run, and hand the sweeper a row addressing the wrong
// library or the wrong key — which is exactly the write a delete-by-key
// sweep must not make against a key still in use, so this also checks
// Referenced lands true/false for the two rows a real sweep would tell
// apart.
func TestSelectDueRoundTripsDistinctFieldsPerRow(t *testing.T) {
	d := repotest.New(t)
	libs := repo.NewLibraryRepo(d)
	files := repo.NewFileRepo(d)
	pr := repo.NewPendingOrphanRepo(d)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Due Round Trip", "due-round-trip", "/tmp/due-round-trip", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// A live files row at this key — the sweeper must not delete a
	// pending_orphans row addressing it.
	if _, err := files.Insert(ctx, model.File{
		LibraryID: lib.ID, Location: "due-round-trip/still-referenced.epub", Format: "EPUB",
	}); err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	const bookID = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	past := time.Now().Add(-time.Hour)
	rows := []repo.PendingOrphanInsert{
		{
			LibraryID: lib.ID, Key: "due-round-trip/still-referenced.epub", EligibleAt: past,
			Reason: repo.ReasonOrphanBookDelete, BookID: pointerTo(bookID),
		},
		{
			LibraryID: lib.ID, Key: "due-round-trip/no-longer-referenced.epub", EligibleAt: past,
			Reason: repo.ReasonOrphanBookDelete, BookID: pointerTo(bookID),
		},
	}
	if err := pr.Insert(ctx, rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	due, err := pr.SelectDue(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("SelectDue: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("SelectDue returned %d rows, want 2", len(due))
	}

	byKey := map[string]repo.DuePendingOrphan{}
	for _, o := range due {
		byKey[o.Key] = o
	}

	referenced, ok := byKey["due-round-trip/still-referenced.epub"]
	if !ok {
		t.Fatalf("no row for the referenced key; keys seen: %v", keysOf(byKey))
	}
	if referenced.LibraryID != lib.ID {
		t.Errorf("referenced row LibraryID = %q, want %q", referenced.LibraryID, lib.ID)
	}
	if referenced.Reason != repo.ReasonOrphanBookDelete {
		t.Errorf("referenced row Reason = %q, want %q", referenced.Reason, repo.ReasonOrphanBookDelete)
	}
	if referenced.BookID == nil || *referenced.BookID != bookID {
		t.Errorf("referenced row BookID = %v, want %q", referenced.BookID, bookID)
	}
	if !referenced.Referenced {
		t.Error("referenced row: Referenced = false, want true — a live files row names this key")
	}

	unreferenced, ok := byKey["due-round-trip/no-longer-referenced.epub"]
	if !ok {
		t.Fatalf("no row for the unreferenced key; keys seen: %v", keysOf(byKey))
	}
	if unreferenced.LibraryID != lib.ID {
		t.Errorf("unreferenced row LibraryID = %q, want %q", unreferenced.LibraryID, lib.ID)
	}
	if unreferenced.Referenced {
		t.Error("unreferenced row: Referenced = true, want false — no files row names this key")
	}
}

func pointerTo(s string) *string { return &s }

func keysOf(m map[string]repo.DuePendingOrphan) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
