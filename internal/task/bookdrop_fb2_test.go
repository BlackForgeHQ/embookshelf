// SPDX-License-Identifier: AGPL-3.0-or-later

package task

// The BookDrop pipeline driven end to end for .fb2 (#312): Intake (the
// row a watcher or upload would create), BookDropIngest (the real
// fileproc.Dispatch + ExtractBook pass — the thing that used to answer
// "no processor for FB2 yet" before this issue wired one), and Approve
// (placement + the books row). A real Postgres schema throughout and a
// real local storage backend, via bookDropPipeline
// (bookdrop_pipeline_test.go) — the same harness the comic and MOBI
// pipeline tests use — run with an .fb2 file so the processor is
// exercised by the pipeline that actually calls it in production, not
// just by fileproc's own unit tests.

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
)

// fb2Fixture is a small but complete FictionBook document: title-info
// with a genre, one author, an annotation, and a coverpage pointing at a
// base64 binary — everything the acceptance criteria ask the processor
// to pull out.
func fb2Fixture() []byte {
	cover := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'} // fake JPEG
	b64 := base64.StdEncoding.EncodeToString(cover)
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:xlink="http://www.w3.org/1999/xlink">
<description><title-info>
<genre>sf</genre>
<author><first-name>Frank</first-name><last-name>Herbert</last-name></author>
<book-title>Dune</book-title>
<annotation><p>A desert planet.</p></annotation>
<lang>en</lang>
<coverpage><image xlink:href="#cover.jpg"/></coverpage>
</title-info></description>
<body><section><p>Chapter text.</p></section></body>
<binary id="cover.jpg" content-type="image/jpeg">` + b64 + `</binary>
</FictionBook>`)
}

func TestBookDropIngest_FB2_IntakeIngestApprove(t *testing.T) {
	ctx := context.Background()
	p := newBookDropPipeline(t)
	fb2Path := p.stage(t, "dune.fb2", fb2Fixture())

	// --- intake: the watcher's path -------------------------------------
	item, created, err := p.svc.Intake(ctx, fb2Path)
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if !created {
		t.Fatal("Intake: created = false, want true")
	}
	if item.Format != "FB2" {
		t.Fatalf("Intake: item.Format = %q, want FB2", item.Format)
	}

	// --- ingest: the real fileproc.Dispatch + ExtractBook pass ----------
	// Before #312 this failed the item with fileproc.NoProcessorError;
	// this is the call that would have produced that refusal.
	if err := BookDropIngest(ctx, jobs.BookDropIngestArgs{ItemID: item.ID}, BookDropDeps{
		Svc:      p.svc,
		Resolver: p.resolver,
	}); err != nil {
		t.Fatalf("BookDropIngest: %v", err)
	}

	ingested, err := p.svc.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get after ingest: %v", err)
	}
	if ingested.State != model.BookDropReady {
		t.Fatalf("state = %q (error %q), want ready", ingested.State, ingested.ErrorMsg)
	}
	if ingested.Title != "Dune" {
		t.Errorf("Title = %q, want Dune", ingested.Title)
	}
	if ingested.Author != "Frank Herbert" {
		t.Errorf("Author = %q, want %q", ingested.Author, "Frank Herbert")
	}
	if !ingested.HasCover {
		t.Error("expected HasCover after ingest")
	}
	if ingested.ContentHash == nil {
		t.Error("expected a content hash to have been recorded")
	}

	// --- approve: placement + the books row ------------------------------
	book, err := p.svc.Approve(ctx, item.ID, p.lib.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if book.Format != "FB2" {
		t.Errorf("book.Format = %q, want FB2", book.Format)
	}
	if book.Title != "Dune" {
		t.Errorf("book.Title = %q, want Dune", book.Title)
	}
	if book.Author != "Frank Herbert" {
		t.Errorf("book.Author = %q, want %q", book.Author, "Frank Herbert")
	}
	if !book.HasCover {
		t.Error("expected the approved book to carry a cover")
	}

	wantLocation := filepath.Join("Frank Herbert", "Dune", "dune.fb2")
	if book.Path != wantLocation {
		t.Errorf("book.Path = %q, want %q", book.Path, wantLocation)
	}
	if _, err := os.Stat(filepath.Join(p.libRoot, wantLocation)); err != nil {
		t.Errorf("approved bytes are not inside the library: %v", err)
	}

	final, err := p.svc.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get after approve: %v", err)
	}
	if final.State != model.BookDropImported {
		t.Errorf("state = %q, want imported", final.State)
	}
}
