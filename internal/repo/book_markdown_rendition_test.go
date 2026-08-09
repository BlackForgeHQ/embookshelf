// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// renditionFixture creates a library + PDF book and returns the book id.
func renditionFixture(t *testing.T, d *db.DB) (string, *repo.BookMarkdownRenditionRepo) {
	t.Helper()
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Renditions", "renditions", "/tmp/renditions", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	b, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Continuous Architecture", Author: "Murat Erder", Format: "PDF",
		Path: "ca.pdf",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	return b.ID, repo.NewBookMarkdownRenditionRepo(d)
}

func TestMarkdownRenditionLifecycle(t *testing.T) {
	d := repotest.New(t)
	bookID, r := renditionFixture(t, d)
	ctx := context.Background()

	if err := r.Start(ctx, bookID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := r.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.State != model.MarkdownRenditionPending || got.Error != "" {
		t.Fatalf("after Start: %+v", got)
	}

	if err := r.MarkRunning(ctx, bookID); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	hash := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := r.MarkReady(ctx, bookID, "Author/Title/Title.md", 1234, hash, "0.1.0"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	got, err = r.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.State != model.MarkdownRenditionReady {
		t.Fatalf("State = %q, want ready", got.State)
	}
	if got.Location != "Author/Title/Title.md" || got.SizeBytes != 1234 {
		t.Fatalf("row = %+v", got)
	}
	if !bytes.Equal(got.SourceContentHash, hash) || got.ConverterVersion != "0.1.0" {
		t.Fatalf("provenance = %x / %q", got.SourceContentHash, got.ConverterVersion)
	}
}

// TestMarkdownRenditionFailureIsLoudAndVerbatim — the error channel is
// the feature (ADR-0033 §5): what the worker records is exactly what the
// status API surfaces.
func TestMarkdownRenditionFailureIsLoudAndVerbatim(t *testing.T) {
	d := repotest.New(t)
	bookID, r := renditionFixture(t, d)
	ctx := context.Background()

	if err := r.Start(ctx, bookID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const msg = "converter extension is not configured"
	if err := r.MarkFailed(ctx, bookID, msg); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err := r.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.State != model.MarkdownRenditionFailed || got.Error != msg {
		t.Fatalf("row = %+v", got)
	}

	// Re-triggering clears the error and goes back to pending, keeping
	// the last good artifact fields until a new ready overwrites them.
	if err := r.Start(ctx, bookID); err != nil {
		t.Fatalf("Start again: %v", err)
	}
	got, err = r.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.State != model.MarkdownRenditionPending || got.Error != "" {
		t.Fatalf("after restart: %+v", got)
	}
}

func TestMarkdownRenditionGetMissing(t *testing.T) {
	d := repotest.New(t)
	bookID, r := renditionFixture(t, d)

	_, err := r.GetByBookID(context.Background(), bookID)
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMarkdownRenditionDeletedWithBook(t *testing.T) {
	d := repotest.New(t)
	bookID, r := renditionFixture(t, d)
	ctx := context.Background()

	if err := r.Start(ctx, bookID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := d.PG.Exec(ctx, "DELETE FROM books WHERE id = $1", bookID); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	if _, err := r.GetByBookID(ctx, bookID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("row survived its book: err = %v", err)
	}
}
