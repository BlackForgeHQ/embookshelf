// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/task"
)

// newCoversBackfillDeps creates a CoversBackfillDeps wired to a fresh SQLite
// DB and a coverstore rooted at a temp directory.
func newCoversBackfillDeps(t *testing.T) (task.CoversBackfillDeps, *repo.LibraryRepo, *repo.BookRepo, string) {
	t.Helper()
	d := repotest.New(t)
	coverRoot := t.TempDir()
	cs := coverstore.New(coverRoot)
	lr := repo.NewLibraryRepo(d)
	br := repo.NewBookRepo(d)
	deps := task.CoversBackfillDeps{
		Books:  br,
		Covers: cs,
	}
	return deps, lr, br, coverRoot
}

// createBookWithLegacyCover creates a library + book row with has_cover=true
// and writes fake cover bytes to the legacy books/<id> path.
func createBookWithLegacyCover(
	t *testing.T,
	lr *repo.LibraryRepo,
	br *repo.BookRepo,
	cs *coverstore.Store,
	coverRoot string,
	data []byte,
) model.Book {
	t.Helper()
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Cover Backfill Lib", "cover-backfill-lib", "/tmp/cbl", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := br.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Legacy Cover Book",
		HasCover:  true,
		CoverMime: "image/jpeg",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}

	// Write cover bytes to the legacy books/<id> path.
	legacyPath := cs.BookPath(book.ID)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll legacy: %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile legacy cover: %v", err)
	}

	return book
}

// TestRunCoversBackfill_noBooksNoOp verifies that 0 books missing hash → no-op.
func TestRunCoversBackfill_noBooksNoOp(t *testing.T) {
	deps, _, _, _ := newCoversBackfillDeps(t)
	if err := task.RunCoversBackfill(context.Background(), deps); err != nil {
		t.Fatalf("no books: got %v, want nil", err)
	}
}

// TestRunCoversBackfill_migratesLegacyCover verifies the happy path:
// 1 book with legacy cover, NULL cover_hash → after backfill: hash computed,
// file at hashed path matches bytes, DB row has cover_hash, legacy file deleted.
func TestRunCoversBackfill_migratesLegacyCover(t *testing.T) {
	deps, lr, br, coverRoot := newCoversBackfillDeps(t)
	cs := coverstore.New(coverRoot)

	coverData := []byte("fake jpeg cover bytes")
	book := createBookWithLegacyCover(t, lr, br, cs, coverRoot, coverData)

	ctx := context.Background()
	if err := task.RunCoversBackfill(ctx, deps); err != nil {
		t.Fatalf("RunCoversBackfill: %v", err)
	}

	// DB row should now have cover_hash set.
	got, err := br.GetByID(ctx, "", book.ID)
	if err != nil {
		t.Fatalf("GetBookByID after backfill: %v", err)
	}
	if len(got.CoverHash) == 0 {
		t.Fatal("cover_hash should be set after backfill, got nil")
	}

	// Hash should match sha256 of the cover data.
	expected := sha256.Sum256(coverData)
	if !bytes.Equal(got.CoverHash, expected[:]) {
		t.Fatalf("CoverHash mismatch: got %x, want %x", got.CoverHash, expected)
	}

	// Hashed path should exist and contain the original bytes.
	hashedPath := cs.HashedPath(got.CoverHash, book.CoverMime)
	onDisk, err := os.ReadFile(hashedPath)
	if err != nil {
		t.Fatalf("ReadFile hashed path %q: %v", hashedPath, err)
	}
	if !bytes.Equal(onDisk, coverData) {
		t.Fatalf("hashed cover bytes mismatch")
	}

	// Legacy path should be removed.
	legacyPath := cs.BookPath(book.ID)
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy cover still exists after backfill: %v", err)
	}
}

// TestRunCoversBackfill_missingLegacyFileSkipped verifies that a book with
// NULL cover_hash but a missing legacy file is skipped gracefully (no DB change).
func TestRunCoversBackfill_missingLegacyFileSkipped(t *testing.T) {
	deps, lr, br, _ := newCoversBackfillDeps(t)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Skip Lib", "skip-lib", "/tmp/sl", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := br.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "No File Book",
		HasCover:  true,
		CoverMime: "image/jpeg",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}

	// Intentionally do NOT write a legacy cover file.
	if err := task.RunCoversBackfill(ctx, deps); err != nil {
		t.Fatalf("RunCoversBackfill: %v", err)
	}

	// DB row should still have nil CoverHash.
	got, err := br.GetByID(ctx, "", book.ID)
	if err != nil {
		t.Fatalf("GetBookByID: %v", err)
	}
	if len(got.CoverHash) != 0 {
		t.Fatalf("CoverHash should remain nil after skip, got %x", got.CoverHash)
	}
}

// TestRunCoversBackfill_idempotent verifies that re-running after success is a
// no-op (the LIMIT-500 query returns no rows).
func TestRunCoversBackfill_idempotent(t *testing.T) {
	deps, lr, br, coverRoot := newCoversBackfillDeps(t)
	cs := coverstore.New(coverRoot)

	coverData := []byte("idempotent cover")
	book := createBookWithLegacyCover(t, lr, br, cs, coverRoot, coverData)

	ctx := context.Background()

	// First run: migrates the cover.
	if err := task.RunCoversBackfill(ctx, deps); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Verify it was set.
	got, err := br.GetByID(ctx, "", book.ID)
	if err != nil {
		t.Fatalf("GetBookByID after first run: %v", err)
	}
	if len(got.CoverHash) == 0 {
		t.Fatal("CoverHash not set after first run")
	}
	firstHash := make([]byte, len(got.CoverHash))
	copy(firstHash, got.CoverHash)

	// Second run: no pending books → no-op.
	if err := task.RunCoversBackfill(ctx, deps); err != nil {
		t.Fatalf("second run: %v", err)
	}

	got2, err := br.GetByID(ctx, "", book.ID)
	if err != nil {
		t.Fatalf("GetBookByID after second run: %v", err)
	}
	if !bytes.Equal(got2.CoverHash, firstHash) {
		t.Fatalf("second run changed CoverHash: got %x want %x", got2.CoverHash, firstHash)
	}
}
