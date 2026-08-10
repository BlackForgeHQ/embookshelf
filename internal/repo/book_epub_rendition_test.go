// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func TestEpubRenditionLifecycle(t *testing.T) {
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
	if got.State != model.MarkdownRenditionReady || got.FileID == nil || *got.FileID != f.ID {
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

	// Failure is verbatim, and the book cascade takes the row with it.
	if err := r.MarkFailed(ctx, b.ID, "markdown is empty: nothing to render"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, _ = r.GetByBookID(ctx, b.ID)
	if got.State != model.MarkdownRenditionFailed || got.Error != "markdown is empty: nothing to render" {
		t.Fatalf("row = %+v", got)
	}
	if _, err := d.PG.Exec(ctx, "DELETE FROM books WHERE id = $1", b.ID); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	if _, err := r.GetByBookID(ctx, b.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("row survived its book: %v", err)
	}
}
