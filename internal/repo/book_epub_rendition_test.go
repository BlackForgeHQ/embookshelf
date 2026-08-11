// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// The shared lifecycle (states, guards, loud failure) is covered by the
// suite in book_rendition_test.go over both artifact shapes. What is
// tested here is the EPUB's own artifact projection: the file_id
// pointer and what happens when the files row it names goes away.

func TestEpubRenditionArtifactProjection(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	files := repo.NewFileRepo(d)
	r := repo.NewBookEpubRenditionRepo(d)

	lib, err := libs.CreateLibrary(ctx, "Epubs", "epubs", "/tmp/epubs", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	b, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "A", Format: "PDF", Path: "dune.pdf",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	if err := r.Start(ctx, b.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.MarkRunning(ctx, b.ID); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	f, err := files.Insert(ctx, model.File{
		LibraryID: lib.ID, BookID: b.ID, Location: "A/Dune/Dune.epub",
		Format: "EPUB", Size: 10,
	})
	if err != nil {
		t.Fatalf("insert files row: %v", err)
	}
	hash := []byte{0xaa, 0xbb}
	if err := r.MarkReady(ctx, b.ID, f.ID, hash, "0.2.0"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}

	got, err := r.GetByBookID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.State != model.RenditionReady || got.FileID == nil || *got.FileID != f.ID {
		t.Fatalf("row = %+v", got)
	}
	if !bytes.Equal(got.SourceContentHash, hash) || got.ConverterVersion != "0.2.0" {
		t.Fatalf("provenance = %x / %q", got.SourceContentHash, got.ConverterVersion)
	}

	// Deleting the files row leaves "was generated, file gone" — the
	// pointer nulls, the row survives (ON DELETE SET NULL).
	if _, err := d.PG.Exec(ctx, "DELETE FROM files WHERE id = $1", f.ID); err != nil {
		t.Fatalf("delete files row: %v", err)
	}
	got, err = r.GetByBookID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByBookID after file delete: %v", err)
	}
	if got.FileID != nil {
		t.Fatalf("FileID = %v, want nil after the files row went", *got.FileID)
	}
}
