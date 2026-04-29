package scan_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/scan"
)

// setupReattach creates a fully-migrated DB and seeds two libraries.
// Returns (fileRepo, lib1ID, lib2ID).
func setupReattach(t *testing.T) (*repo.FileRepo, string, string) {
	t.Helper()
	d := repotest.New(t)
	fr := repo.NewFileRepo(d)
	lr := repo.NewLibraryRepo(d)
	ctx := context.Background()

	lib1, err := lr.CreateLibrary(ctx, "Library One", "lib-one", "/tmp/lib1")
	if err != nil {
		t.Fatalf("CreateLibrary lib1: %v", err)
	}
	lib2, err := lr.CreateLibrary(ctx, "Library Two", "lib-two", "/tmp/lib2")
	if err != nil {
		t.Fatalf("CreateLibrary lib2: %v", err)
	}
	return fr, lib1.ID, lib2.ID
}

func insertTestFile(t *testing.T, fr *repo.FileRepo, libraryID, location string, hash []byte) model.File {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	f := model.File{
		LibraryID:   libraryID,
		Location:    location,
		Size:        100,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
		ContentHash: hash,
	}
	inserted, err := fr.Insert(ctx, f)
	if err != nil {
		t.Fatalf("Insert(%q): %v", location, err)
	}
	return inserted
}

func TestMaybeReattach_NoHashMatches(t *testing.T) {
	fr, lib1ID, _ := setupReattach(t)
	ctx := context.Background()

	// Insert an old row with no matching hash anywhere.
	h := sha256.Sum256([]byte("unique-content"))
	oldRow := insertTestFile(t, fr, lib1ID, "old.epub", nil)

	reattached, err := scan.MaybeReattach(ctx, fr, lib1ID, h[:], "new.epub", oldRow.ID)
	if err != nil {
		t.Fatalf("MaybeReattach unexpected error: %v", err)
	}
	if reattached {
		t.Fatalf("expected (false, nil), got reattached=true")
	}

	// Old row should be unmodified.
	got, err := fr.GetByLocation(ctx, lib1ID, "old.epub")
	if err != nil {
		t.Fatalf("GetByLocation old.epub: %v", err)
	}
	if got.MissingSince != nil {
		t.Errorf("old row MissingSince should be nil, got %v", got.MissingSince)
	}
}

func TestMaybeReattach_EmptyHash(t *testing.T) {
	fr, lib1ID, _ := setupReattach(t)
	ctx := context.Background()

	oldRow := insertTestFile(t, fr, lib1ID, "old.epub", nil)

	reattached, err := scan.MaybeReattach(ctx, fr, lib1ID, nil, "new.epub", oldRow.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reattached {
		t.Fatalf("expected (false, nil) for empty hash")
	}
}

func TestMaybeReattach_HashMatchInDifferentLibrary(t *testing.T) {
	fr, lib1ID, lib2ID := setupReattach(t)
	ctx := context.Background()

	h := sha256.Sum256([]byte("shared-content"))

	// The matching row lives in lib2, not lib1.
	insertTestFile(t, fr, lib2ID, "lib2/file.epub", h[:])

	oldRow := insertTestFile(t, fr, lib1ID, "old.epub", nil)

	reattached, err := scan.MaybeReattach(ctx, fr, lib1ID, h[:], "new.epub", oldRow.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reattached {
		t.Fatalf("expected (false, nil) for match in different library")
	}

	// old row must be untouched.
	got, err := fr.GetByLocation(ctx, lib1ID, "old.epub")
	if err != nil {
		t.Fatalf("GetByLocation: %v", err)
	}
	if got.MissingSince != nil {
		t.Errorf("old row should not be marked missing")
	}
}

func TestMaybeReattach_HashMatchesOldRowOnly(t *testing.T) {
	fr, lib1ID, _ := setupReattach(t)
	ctx := context.Background()

	h := sha256.Sum256([]byte("self-match"))

	// The only row with this hash IS the old row itself.
	oldRow := insertTestFile(t, fr, lib1ID, "old.epub", h[:])

	reattached, err := scan.MaybeReattach(ctx, fr, lib1ID, h[:], "new.epub", oldRow.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reattached {
		t.Fatalf("expected (false, nil) when hash only matches oldRowID")
	}
}

func TestMaybeReattach_Rename(t *testing.T) {
	fr, lib1ID, _ := setupReattach(t)
	ctx := context.Background()

	h := sha256.Sum256([]byte("book-content"))

	// The "canonical" row already exists at a.epub with the hash.
	canonRow := insertTestFile(t, fr, lib1ID, "a.epub", h[:])

	// The "stale" row — the previous location that is now being re-scanned.
	oldRow := insertTestFile(t, fr, lib1ID, "b_old.epub", nil)

	// Simulate: file was renamed from b_old.epub to b_new.epub.
	// The re-hash of b_new.epub matches canonRow's hash.
	reattached, err := scan.MaybeReattach(ctx, fr, lib1ID, h[:], "b_new.epub", oldRow.ID)
	if err != nil {
		t.Fatalf("MaybeReattach: %v", err)
	}
	if !reattached {
		t.Fatalf("expected reattached=true for rename scenario")
	}

	// canonRow should now live at b_new.epub.
	updated, err := fr.GetByLocation(ctx, lib1ID, "b_new.epub")
	if err != nil {
		t.Fatalf("GetByLocation b_new.epub: %v", err)
	}
	if updated.ID != canonRow.ID {
		t.Errorf("expected canonRow ID %q at b_new.epub, got %q", canonRow.ID, updated.ID)
	}

	// old row should be marked missing.
	allFiles, err := fr.ListByLibrary(ctx, lib1ID)
	if err != nil {
		t.Fatalf("ListByLibrary: %v", err)
	}
	var oldFound *model.File
	for i := range allFiles {
		if allFiles[i].ID == oldRow.ID {
			oldFound = &allFiles[i]
			break
		}
	}
	if oldFound == nil {
		t.Fatal("old row not found in ListByLibrary")
	}
	if oldFound.MissingSince == nil {
		t.Errorf("old row should have MissingSince set after reattach")
	}
}

func TestMaybeReattach_NewLocationTaken(t *testing.T) {
	fr, lib1ID, _ := setupReattach(t)
	ctx := context.Background()

	h := sha256.Sum256([]byte("taken-content"))

	// The row whose hash matches (the one that would be reattached).
	insertTestFile(t, fr, lib1ID, "match.epub", h[:])

	// A third row already sitting at the target location.
	insertTestFile(t, fr, lib1ID, "target.epub", nil)

	// The stale row.
	oldRow := insertTestFile(t, fr, lib1ID, "stale.epub", nil)

	// Reattach would try to move match.epub → target.epub, but target.epub
	// is already taken by a third row.
	_, err := scan.MaybeReattach(ctx, fr, lib1ID, h[:], "target.epub", oldRow.ID)
	if err == nil {
		t.Fatal("expected error when new location is already taken")
	}
	if !errors.Is(err, repo.ErrFileLocationTaken) {
		t.Fatalf("expected ErrFileLocationTaken, got %v", err)
	}
}
