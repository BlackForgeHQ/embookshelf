// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func TestFileRepo_crud(t *testing.T) {
	d := repotest.New(t)
	fr := repo.NewFileRepo(d)
	lr := repo.NewLibraryRepo(d)
	ctx := context.Background()

	// Seed a library that files can reference.
	lib, err := lr.CreateLibrary(ctx, "Test Library", "test-library", "/tmp/test", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	// --- 1. Insert + GetByLocation roundtrip ---
	now := time.Now().UTC().Truncate(time.Millisecond)
	f := model.File{
		LibraryID:   lib.ID,
		Location:    "subdir/book.epub",
		Size:        1024,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	}
	inserted, err := fr.Insert(ctx, f)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if inserted.ID == "" {
		t.Fatal("Insert returned empty ID")
	}
	if inserted.LibraryID != lib.ID {
		t.Fatalf("LibraryID=%q want %q", inserted.LibraryID, lib.ID)
	}
	if inserted.Location != f.Location {
		t.Fatalf("Location=%q want %q", inserted.Location, f.Location)
	}
	if inserted.Format != "EPUB" {
		t.Fatalf("Format=%q want EPUB", inserted.Format)
	}
	if inserted.ContentHash != nil {
		t.Fatal("ContentHash should be nil on fresh insert")
	}

	got, err := fr.GetByLocation(ctx, lib.ID, "subdir/book.epub")
	if err != nil {
		t.Fatalf("GetByLocation: %v", err)
	}
	if got.ID != inserted.ID {
		t.Fatalf("GetByLocation ID=%q want %q", got.ID, inserted.ID)
	}

	// --- 2. GetByLocation not found ---
	_, err = fr.GetByLocation(ctx, lib.ID, "nonexistent/path.epub")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetByLocation missing: got %v, want ErrNotFound", err)
	}

	// --- 3. ExistsByLocation true/false ---
	exists, err := fr.ExistsByLocation(ctx, lib.ID, "subdir/book.epub")
	if err != nil {
		t.Fatalf("ExistsByLocation: %v", err)
	}
	if !exists {
		t.Fatal("ExistsByLocation: should be true for existing file")
	}
	exists, err = fr.ExistsByLocation(ctx, lib.ID, "no/such/file.epub")
	if err != nil {
		t.Fatalf("ExistsByLocation missing: %v", err)
	}
	if exists {
		t.Fatal("ExistsByLocation: should be false for missing file")
	}

	// --- 4. GetByContentHash returns multiple ---
	// Hash two different files with the same content so they share a hash.
	h := sha256.Sum256([]byte("shared content"))
	hashBytes := h[:]

	f2 := model.File{
		LibraryID:   lib.ID,
		Location:    "copy1.pdf",
		Size:        100,
		Mtime:       now,
		Format:      "PDF",
		LastScanned: now,
		ContentHash: hashBytes,
	}
	f3 := model.File{
		LibraryID:   lib.ID,
		Location:    "copy2.pdf",
		Size:        100,
		Mtime:       now,
		Format:      "PDF",
		LastScanned: now,
		ContentHash: hashBytes,
	}
	ins2, err := fr.Insert(ctx, f2)
	if err != nil {
		t.Fatalf("Insert f2: %v", err)
	}
	ins3, err := fr.Insert(ctx, f3)
	if err != nil {
		t.Fatalf("Insert f3: %v", err)
	}
	byHash, err := fr.GetByContentHash(ctx, hashBytes)
	if err != nil {
		t.Fatalf("GetByContentHash: %v", err)
	}
	if len(byHash) != 2 {
		t.Fatalf("GetByContentHash len=%d want 2", len(byHash))
	}
	ids := map[string]bool{ins2.ID: true, ins3.ID: true}
	for _, r := range byHash {
		if !ids[r.ID] {
			t.Fatalf("GetByContentHash returned unexpected ID %q", r.ID)
		}
		if !bytes.Equal(r.ContentHash, hashBytes) {
			t.Fatalf("GetByContentHash content_hash mismatch")
		}
	}

	// --- 5. SetContentHash transitions NULL → set ---
	// ListPendingHash should return the file we inserted without a hash.
	pending, err := fr.ListPendingHash(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingHash: %v", err)
	}
	// inserted (f) has NULL hash; f2 and f3 already have one.
	if len(pending) != 1 {
		t.Fatalf("ListPendingHash len=%d want 1", len(pending))
	}
	if pending[0].ID != inserted.ID {
		t.Fatalf("ListPendingHash returned wrong file ID %q", pending[0].ID)
	}

	newHash := sha256.Sum256([]byte("new content"))
	newMtime := now.Add(time.Second)
	if err := fr.SetContentHash(ctx, inserted.ID, newHash[:], 2048, newMtime); err != nil {
		t.Fatalf("SetContentHash: %v", err)
	}
	// Confirm hash is now set.
	updated, err := fr.GetByLocation(ctx, lib.ID, "subdir/book.epub")
	if err != nil {
		t.Fatalf("GetByLocation after SetContentHash: %v", err)
	}
	if !bytes.Equal(updated.ContentHash, newHash[:]) {
		t.Fatalf("ContentHash mismatch after SetContentHash")
	}
	if updated.Size != 2048 {
		t.Fatalf("Size=%d want 2048", updated.Size)
	}

	// --- 6. ListPendingHash respects batch size ---
	// Insert 3 more files without a hash.
	for i := 0; i < 3; i++ {
		_, err := fr.Insert(ctx, model.File{
			LibraryID:   lib.ID,
			Location:    "batch/file" + string(rune('a'+i)) + ".epub",
			Size:        10,
			Mtime:       now,
			Format:      "EPUB",
			LastScanned: now,
		})
		if err != nil {
			t.Fatalf("Insert batch[%d]: %v", i, err)
		}
	}
	limited, err := fr.ListPendingHash(ctx, 2)
	if err != nil {
		t.Fatalf("ListPendingHash batchSize=2: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListPendingHash batch limit=%d want 2", len(limited))
	}

	// --- 7. MarkScanned updates last_scanned ---
	if err := fr.MarkScanned(ctx, inserted.ID); err != nil {
		t.Fatalf("MarkScanned: %v", err)
	}
	// We can't assert the exact timestamp but we can confirm no error was returned.

	// --- 8. Insert collision → ErrFileLocationTaken ---
	_, err = fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "subdir/book.epub", // same as inserted
		Size:        1,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if !errors.Is(err, repo.ErrFileLocationTaken) {
		t.Fatalf("dup insert: got %v, want ErrFileLocationTaken", err)
	}

	// --- 9. MarkMissing round-trip ---
	markFile, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "missing/book.epub",
		Size:        512,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert markFile: %v", err)
	}
	if markFile.MissingSince != nil {
		t.Fatal("MissingSince should be nil on fresh insert")
	}
	markTime := time.Now().UTC().Truncate(time.Second)
	if err := fr.MarkMissing(ctx, markFile.ID, markTime); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	byLib, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary after MarkMissing: %v", err)
	}
	var found *model.File
	for i := range byLib {
		if byLib[i].ID == markFile.ID {
			found = &byLib[i]
			break
		}
	}
	if found == nil {
		t.Fatal("ListByLibrary: markFile not found")
	}
	if found.MissingSince == nil {
		t.Fatal("MissingSince should be non-nil after MarkMissing")
	}
	if !found.MissingSince.Truncate(time.Second).Equal(markTime) {
		t.Fatalf("MissingSince=%v want ~%v", found.MissingSince, markTime)
	}

	// --- 10. ClearMissing ---
	if err := fr.ClearMissing(ctx, markFile.ID); err != nil {
		t.Fatalf("ClearMissing: %v", err)
	}
	afterClear, err := fr.GetByLocation(ctx, lib.ID, "missing/book.epub")
	if err != nil {
		t.Fatalf("GetByLocation after ClearMissing: %v", err)
	}
	if afterClear.MissingSince != nil {
		t.Fatalf("MissingSince should be nil after ClearMissing, got %v", afterClear.MissingSince)
	}

	// --- 11. DeleteMissingOlderThan ---
	// Insert a file that went missing 25h ago (should be deleted).
	old := time.Now().Add(-25 * time.Hour)
	oldFile, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "old/missing.epub",
		Size:        100,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert oldFile: %v", err)
	}
	if err := fr.MarkMissing(ctx, oldFile.ID, old); err != nil {
		t.Fatalf("MarkMissing oldFile: %v", err)
	}
	// Insert a file that went missing 1h ago (should survive 24h TTL).
	recent := time.Now().Add(-1 * time.Hour)
	recentFile, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "recent/missing.epub",
		Size:        100,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert recentFile: %v", err)
	}
	if err := fr.MarkMissing(ctx, recentFile.ID, recent); err != nil {
		t.Fatalf("MarkMissing recentFile: %v", err)
	}
	deleted, err := fr.DeleteMissingOlderThan(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("DeleteMissingOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteMissingOlderThan count=%d want 1", deleted)
	}
	// recentFile should still exist.
	_, err = fr.GetByLocation(ctx, lib.ID, "recent/missing.epub")
	if err != nil {
		t.Fatalf("recentFile should survive TTL: %v", err)
	}
	// oldFile should be gone.
	_, err = fr.GetByLocation(ctx, lib.ID, "old/missing.epub")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("oldFile should be purged, got %v", err)
	}

	// --- 12. ListByLibrary isolation ---
	lib2, err := lr.CreateLibrary(ctx, "Other Library", "other-library", "/tmp/other", nil)
	if err != nil {
		t.Fatalf("CreateLibrary lib2: %v", err)
	}
	_, err = fr.Insert(ctx, model.File{
		LibraryID:   lib2.ID,
		Location:    "other/book.epub",
		Size:        10,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert lib2 file: %v", err)
	}
	lib1Files, err := fr.ListByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListByLibrary lib: %v", err)
	}
	for _, f := range lib1Files {
		if f.LibraryID != lib.ID {
			t.Fatalf("ListByLibrary returned file from wrong library: %q", f.LibraryID)
		}
	}
	lib2Files, err := fr.ListByLibrary(ctx, lib2.ID)
	if err != nil {
		t.Fatalf("ListByLibrary lib2: %v", err)
	}
	if len(lib2Files) != 1 {
		t.Fatalf("ListByLibrary lib2 count=%d want 1", len(lib2Files))
	}

	// --- 13. UpdateLocation happy path + conflict ---
	moveFile, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "move/source.epub",
		Size:        200,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert moveFile: %v", err)
	}
	if err := fr.UpdateLocation(ctx, moveFile.ID, "move/dest.epub"); err != nil {
		t.Fatalf("UpdateLocation: %v", err)
	}
	_, err = fr.GetByLocation(ctx, lib.ID, "move/dest.epub")
	if err != nil {
		t.Fatalf("GetByLocation after UpdateLocation: %v", err)
	}
	_, err = fr.GetByLocation(ctx, lib.ID, "move/source.epub")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("old location should be gone after UpdateLocation, got %v", err)
	}
	// Conflict: try to move to an existing location.
	conflictTarget, err := fr.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		Location:    "move/conflict.epub",
		Size:        10,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert conflictTarget: %v", err)
	}
	err = fr.UpdateLocation(ctx, conflictTarget.ID, "move/dest.epub")
	if !errors.Is(err, repo.ErrFileLocationTaken) {
		t.Fatalf("UpdateLocation conflict: got %v, want ErrFileLocationTaken", err)
	}
}
