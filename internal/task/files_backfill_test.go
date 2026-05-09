// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/task"
)

// newTestLibStore builds a LibraryStore wired to a real LibraryRepo and a
// ConstantResolver returning fs for every backend lookup. Mirrors the
// production main.go shape with everything stitched in-memory.
func newTestLibStore(lr *repo.LibraryRepo, fs storage.Storage) service.LibraryStore {
	return service.NewLibraryStore(service.LibraryStoreDeps{
		Libs:     lr,
		Resolver: storage.ConstantResolver{S: fs},
	})
}

// newTestDeps creates a FilesBackfillDeps wired to a fresh SQLite DB and a
// LocalFS rooted at "/" (matching the production main.go configuration).
// File keys are therefore absolute paths like "<tmpDir>/<name>".
func newTestDeps(t *testing.T) (task.FilesBackfillDeps, *repo.LibraryRepo) {
	t.Helper()
	d := repotest.NewWithDialect(t, "sqlite")
	// Root the storage at "/" — same as production. joinKey will build
	// keys like "/tmp/.../filename.epub" which LocalFS resolves correctly.
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	lr := repo.NewLibraryRepo(d)
	deps := task.FilesBackfillDeps{
		Files:    repo.NewFileRepo(d),
		LibStore: newTestLibStore(lr, fs),
	}
	return deps, lr
}

// seedLibrary creates a library whose Path equals tmpDir so joinKey resolves
// file locations against that directory.
func seedLibrary(t *testing.T, lr *repo.LibraryRepo, tmpDir string) model.Library {
	t.Helper()
	lib, err := lr.CreateLibrary(context.Background(), "Test Library", "test-lib", tmpDir, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return lib
}

// writeFile creates a file at <tmpDir>/<name> with the given content.
func writeFile(t *testing.T, tmpDir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %q: %v", name, err)
	}
}

// insertFile inserts a files row with content_hash = NULL (no hash yet).
func insertFile(t *testing.T, fr *repo.FileRepo, libID, location string) model.File {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	f, err := fr.Insert(context.Background(), model.File{
		LibraryID:   libID,
		Location:    location,
		Size:        0,
		Mtime:       now,
		Format:      "EPUB",
		LastScanned: now,
	})
	if err != nil {
		t.Fatalf("Insert %q: %v", location, err)
	}
	return f
}

// TestRunFilesBackfill_nilDeps verifies that nil LibStore or Files returns
// nil immediately (guards against incomplete wiring at startup).
func TestRunFilesBackfill_nilDeps(t *testing.T) {
	ctx := context.Background()

	// nil LibStore
	if err := task.RunFilesBackfill(ctx, task.FilesBackfillDeps{
		Files:    repo.NewFileRepo(nil),
		LibStore: nil,
	}); err != nil {
		t.Fatalf("nil LibStore: got %v, want nil", err)
	}

	// nil Files
	d := repotest.NewWithDialect(t, "sqlite")
	fs, _ := local.New("/")
	lr := repo.NewLibraryRepo(d)
	if err := task.RunFilesBackfill(ctx, task.FilesBackfillDeps{
		Files:    nil,
		LibStore: newTestLibStore(lr, fs),
	}); err != nil {
		t.Fatalf("nil Files: got %v, want nil", err)
	}
}

// TestRunFilesBackfill_noPendingRows verifies that an empty pending set
// returns nil without logging anything disruptive.
func TestRunFilesBackfill_noPendingRows(t *testing.T) {
	tmpDir := t.TempDir()
	deps, lr := newTestDeps(t)
	_ = seedLibrary(t, lr, tmpDir)

	// No files inserted → nothing pending.
	if err := task.RunFilesBackfill(context.Background(), deps); err != nil {
		t.Fatalf("empty pending: got %v, want nil", err)
	}
}

// TestRunFilesBackfill_hashesThreeFiles exercises the happy path:
// three files with NULL hash are all hashed correctly.
func TestRunFilesBackfill_hashesThreeFiles(t *testing.T) {
	tmpDir := t.TempDir()
	deps, lr := newTestDeps(t)
	lib := seedLibrary(t, lr, tmpDir)

	contents := []string{"alpha content", "beta content", "gamma content"}
	names := []string{"alpha.epub", "beta.epub", "gamma.epub"}

	for i, name := range names {
		writeFile(t, tmpDir, name, contents[i])
		insertFile(t, deps.Files, lib.ID, name)
	}

	ctx := context.Background()
	if err := task.RunFilesBackfill(ctx, deps); err != nil {
		t.Fatalf("RunFilesBackfill: %v", err)
	}

	// All three should now have a content_hash.
	pending, err := deps.Files.ListPendingHash(ctx, 100)
	if err != nil {
		t.Fatalf("ListPendingHash: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("still %d pending rows after backfill; want 0", len(pending))
	}

	// Verify the hashes match the actual file content.
	for i, name := range names {
		expected := sha256.Sum256([]byte(contents[i]))
		got, err := deps.Files.GetByLocation(ctx, lib.ID, name)
		if err != nil {
			t.Fatalf("GetByLocation %q: %v", name, err)
		}
		if got.ContentHash == nil {
			t.Fatalf("file %q: ContentHash is nil after backfill", name)
		}
		if !bytes.Equal(got.ContentHash, expected[:]) {
			t.Fatalf("file %q: ContentHash mismatch\n  got  %x\n  want %x",
				name, got.ContentHash, expected)
		}
	}
}

// TestRunFilesBackfill_missingFileSkipped verifies that a row pointing to a
// file that doesn't exist on disk stays NULL while the other rows are filled.
func TestRunFilesBackfill_missingFileSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	deps, lr := newTestDeps(t)
	lib := seedLibrary(t, lr, tmpDir)

	// Write two real files, leave the third missing.
	writeFile(t, tmpDir, "present1.epub", "present content 1")
	writeFile(t, tmpDir, "present2.epub", "present content 2")
	// "missing.epub" is intentionally not created on disk.

	present1 := insertFile(t, deps.Files, lib.ID, "present1.epub")
	missingRow := insertFile(t, deps.Files, lib.ID, "missing.epub")
	present2 := insertFile(t, deps.Files, lib.ID, "present2.epub")

	ctx := context.Background()
	if err := task.RunFilesBackfill(ctx, deps); err != nil {
		t.Fatalf("RunFilesBackfill: %v", err)
	}

	// present1 and present2 should have hashes.
	f1, err := deps.Files.GetByLocation(ctx, lib.ID, "present1.epub")
	if err != nil {
		t.Fatalf("GetByLocation present1: %v", err)
	}
	if f1.ContentHash == nil {
		t.Fatalf("present1.epub: ContentHash should be set, got nil")
	}
	_ = present1

	f2, err := deps.Files.GetByLocation(ctx, lib.ID, "present2.epub")
	if err != nil {
		t.Fatalf("GetByLocation present2: %v", err)
	}
	if f2.ContentHash == nil {
		t.Fatalf("present2.epub: ContentHash should be set, got nil")
	}
	_ = present2

	// The missing file row should still have NULL hash.
	pending, err := deps.Files.ListPendingHash(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingHash: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count=%d want 1 (the missing file)", len(pending))
	}
	if pending[0].ID != missingRow.ID {
		t.Fatalf("pending row ID=%q want %q", pending[0].ID, missingRow.ID)
	}
}

// TestRunFilesBackfill_idempotent verifies that running the backfill a second
// time is a no-op — rows with hashes are not re-processed.
func TestRunFilesBackfill_idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	deps, lr := newTestDeps(t)
	lib := seedLibrary(t, lr, tmpDir)

	writeFile(t, tmpDir, "book.epub", "stable content")
	insertFile(t, deps.Files, lib.ID, "book.epub")

	ctx := context.Background()

	// First run hashes the file.
	if err := task.RunFilesBackfill(ctx, deps); err != nil {
		t.Fatalf("first run: %v", err)
	}
	f, err := deps.Files.GetByLocation(ctx, lib.ID, "book.epub")
	if err != nil {
		t.Fatalf("GetByLocation after first run: %v", err)
	}
	hashAfterFirst := make([]byte, len(f.ContentHash))
	copy(hashAfterFirst, f.ContentHash)

	// Second run: no pending rows → returns immediately.
	if err := task.RunFilesBackfill(ctx, deps); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// Hash unchanged.
	f2, err := deps.Files.GetByLocation(ctx, lib.ID, "book.epub")
	if err != nil {
		t.Fatalf("GetByLocation after second run: %v", err)
	}
	if !bytes.Equal(f2.ContentHash, hashAfterFirst) {
		t.Fatalf("second run changed hash: got %x want %x", f2.ContentHash, hashAfterFirst)
	}
}
