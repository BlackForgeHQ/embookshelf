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
// tested here is markdown's own: the artifact projection MarkReady
// writes, and the bulk-conversion queries.

// TestMarkdownRenditionArtifactProjection — MarkReady records where the
// bytes landed and the provenance that decides staleness later.
func TestMarkdownRenditionArtifactProjection(t *testing.T) {
	d := repotest.New(t)
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	r := repo.NewBookMarkdownRenditionRepo(d)
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

	if err := r.Start(ctx, b.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.MarkRunning(ctx, b.ID); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	hash := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := r.MarkReady(ctx, b.ID, "Author/Title/Title.md", 1234, hash, "0.1.0"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}

	got, err := r.GetByBookID(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.State != model.RenditionReady {
		t.Fatalf("State = %q, want ready", got.State)
	}
	if got.Location != "Author/Title/Title.md" || got.SizeBytes != 1234 {
		t.Fatalf("row = %+v", got)
	}
	if !bytes.Equal(got.SourceContentHash, hash) || got.ConverterVersion != "0.1.0" {
		t.Fatalf("provenance = %x / %q", got.SourceContentHash, got.ConverterVersion)
	}
}

// TestConversionCandidatesAndCoverage — the two bulk-run queries agree
// on who counts: convertible books only, absent-or-failed are
// candidates, ready is left alone.
func TestConversionCandidatesAndCoverage(t *testing.T) {
	d := repotest.New(t)
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	r := repo.NewBookMarkdownRenditionRepo(d)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Bulk", "bulk", "/tmp/bulk", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	mk := func(title, format string) string {
		b, err := books.Create(ctx, model.Book{
			LibraryID: lib.ID, Title: title, Author: "A", Format: format,
			Path: title + ".bin",
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return b.ID
	}

	virgin := mk("Virgin", "PDF")
	failed := mk("Failed", "PDF")
	ready := mk("Ready", "PDF")
	converting := mk("Converting", "PDF")
	mk("Epub", "EPUB") // never a candidate: not convertible

	if err := r.Start(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkFailed(ctx, failed, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, ready); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkRunning(ctx, ready); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkReady(ctx, ready, "A/Ready/Ready.md", 10, []byte{1}, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, converting); err != nil {
		t.Fatal(err)
	}

	got, err := r.ListConversionCandidates(ctx)
	if err != nil {
		t.Fatalf("ListConversionCandidates: %v", err)
	}
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.BookID] = true
	}
	if len(got) != 2 || !ids[virgin] || !ids[failed] {
		t.Fatalf("candidates = %v, want exactly {virgin, failed}", ids)
	}

	cov, err := r.CountConversionCoverage(ctx)
	if err != nil {
		t.Fatalf("CountConversionCoverage: %v", err)
	}
	want := repo.ConversionCoverage{Total: 4, Ready: 1, Converting: 1, Failed: 1, Unconverted: 1}
	if cov != want {
		t.Fatalf("coverage = %+v, want %+v", cov, want)
	}

	// Candidates() is the counting side of ListConversionCandidates —
	// the two must agree on who a bulk run would touch (#301).
	if cov.Candidates() != len(got) {
		t.Fatalf("Candidates() = %d, ListConversionCandidates found %d", cov.Candidates(), len(got))
	}
}
