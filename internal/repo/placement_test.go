// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestListPlacementsRoundTripsDistinctFieldsPerRow exercises the
// placement view's projection end to end. books contributes four
// adjacent TEXT columns (title, author, format, book_path) and files
// contributes two more (file id, location) right next to them in the
// same SELECT — the placement.go comment on placementProjection spells
// out why that adjacency matters. Every field below is given a value
// distinct from every other field of its type, so a crossed pair among
// them surfaces as a mismatch instead of being masked by two fields
// sharing a value.
func TestListPlacementsRoundTripsDistinctFieldsPerRow(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	files := repo.NewFileRepo(d)

	lib, err := libs.CreateLibrary(ctx, "Placement", "placement", "/tmp/placement", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	b, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Placement Round Trip Title", Author: "Placement Round Trip Author",
		Format: "EPUB", Path: "placement/book.epub",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}

	f, err := files.Insert(ctx, model.File{
		LibraryID: lib.ID, BookID: b.ID, Location: "placement/book/file.bin",
		Size: 123_456, ContentHash: []byte{0xAA, 0xBB, 0xCC, 0xDD}, Format: "EPUB",
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	// A second book with no files row — the LEFT JOIN's other shape,
	// which the 24h missing-purge leaves behind (ADR-0018) and which
	// FileID/Location/Size/ContentHash must all read as empty for.
	orphanBook, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Orphan Round Trip Title", Author: "Orphan Round Trip Author",
		Format: "PDF", Path: "placement/orphan.pdf",
	})
	if err != nil {
		t.Fatalf("Create orphan book: %v", err)
	}

	placements, err := books.ListPlacements(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListPlacements: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("ListPlacements returned %d rows, want 2", len(placements))
	}

	byBook := map[string]repo.Placement{}
	for _, p := range placements {
		byBook[p.BookID] = p
	}

	p, ok := byBook[b.ID]
	if !ok {
		t.Fatalf("no placement for book %s", b.ID)
	}
	if p.Title != "Placement Round Trip Title" || p.Author != "Placement Round Trip Author" ||
		p.Format != "EPUB" || p.BookPath != "placement/book.epub" {
		t.Fatalf("book fields = %+v, want the four distinct strings unswapped", p)
	}
	if !p.HasFileRow() {
		t.Fatal("HasFileRow() = false, want true — a files row exists")
	}
	if p.FileID != f.ID {
		t.Errorf("FileID = %q, want %q", p.FileID, f.ID)
	}
	if p.Location != "placement/book/file.bin" {
		t.Errorf("Location = %q, want placement/book/file.bin", p.Location)
	}
	if p.Size != 123_456 {
		t.Errorf("Size = %d, want 123456", p.Size)
	}
	if string(p.ContentHash) != "\xaa\xbb\xcc\xdd" {
		t.Errorf("ContentHash = %x, want aabbccdd", p.ContentHash)
	}

	op, ok := byBook[orphanBook.ID]
	if !ok {
		t.Fatalf("no placement for orphan book %s", orphanBook.ID)
	}
	if op.Title != "Orphan Round Trip Title" || op.Author != "Orphan Round Trip Author" ||
		op.Format != "PDF" || op.BookPath != "placement/orphan.pdf" {
		t.Fatalf("orphan book fields = %+v, want the four distinct strings unswapped", op)
	}
	if op.HasFileRow() {
		t.Fatal("HasFileRow() = true for a book with no files row")
	}
	if op.FileID != "" || op.Location != "" || op.Size != 0 || op.ContentHash != nil {
		t.Fatalf("orphan placement = %+v, want the LEFT JOIN's empty shape", op)
	}
}
