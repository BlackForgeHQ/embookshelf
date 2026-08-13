// SPDX-License-Identifier: AGPL-3.0-or-later

package task

// The BookDrop pipeline driven end to end for .mobi and .azw3 (#311):
// Intake (the row a watcher or upload would create), BookDropIngest (the
// real fileproc.Dispatch + ExtractBook pass — the thing that used to
// answer "no processor for MOBI yet" before this issue wired one), and
// Approve (placement + the books row). A real Postgres schema throughout
// and a real local storage backend, via bookDropPipeline
// (bookdrop_pipeline_test.go) — the same harness the comic and FB2
// pipeline tests use, run with the two PalmDB-container formats so the
// processor is exercised by the pipeline that actually calls it in
// production rather than only by fileproc's own unit tests.

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
)

// mobiFixture builds a small but structurally complete MOBI/AZW3 file:
// PalmDB container, record 0 with the PalmDOC + MOBI headers and an EXTH
// block carrying the updated title (503), the author (100) and the cover
// offset (201), a text record, and the cover image the offset names.
//
// Assembled here rather than checked in as a binary — a real .mobi is
// megabytes of compressed text around the same few hundred header bytes,
// and none of that text is what this pipeline reads. fileVersion 8 makes
// the same builder emit a KF8/AZW3 file.
func mobiFixture(title, author string, fileVersion uint32) []byte {
	be := binary.BigEndian
	cover := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'} // fake JPEG

	const mobiHeaderLen = 232
	rec0 := make([]byte, 16+mobiHeaderLen)
	be.PutUint16(rec0[0:], 1)     // PalmDOC: no compression
	be.PutUint16(rec0[10:], 4096) // record size
	copy(rec0[16:], "MOBI")
	be.PutUint32(rec0[20:], mobiHeaderLen)
	be.PutUint32(rec0[24:], 2)     // mobi type: BOOK
	be.PutUint32(rec0[28:], 65001) // UTF-8
	be.PutUint32(rec0[36:], fileVersion)
	be.PutUint32(rec0[16+92:], 2)     // first image record index
	be.PutUint32(rec0[16+112:], 0x40) // an EXTH block follows

	// EXTH: 12-byte header, then 8-byte-headed records.
	exthRec := func(typ uint32, data []byte) []byte {
		r := make([]byte, 8)
		be.PutUint32(r[0:], typ)
		be.PutUint32(r[4:], uint32(8+len(data)))
		return append(r, data...)
	}
	coverOffset := make([]byte, 4) // the cover is the first image record
	var body []byte
	body = append(body, exthRec(100, []byte(author))...)
	body = append(body, exthRec(503, []byte(title))...)
	body = append(body, exthRec(201, coverOffset)...)
	exth := make([]byte, 12)
	copy(exth, "EXTH")
	be.PutUint32(exth[4:], uint32(12+len(body)))
	be.PutUint32(exth[8:], 3)
	exth = append(exth, body...)
	for len(exth)%4 != 0 {
		exth = append(exth, 0)
	}
	rec0 = append(rec0, exth...)

	// The full-name field, which EXTH 503 overrides.
	be.PutUint32(rec0[16+68:], uint32(len(rec0)))
	be.PutUint32(rec0[16+72:], uint32(len(title)))
	rec0 = append(rec0, []byte(title)...)

	records := [][]byte{rec0, []byte("book text record"), cover}
	head := make([]byte, 78+8*len(records))
	copy(head[0:], "fixture")
	copy(head[60:], "BOOKMOBI")
	be.PutUint16(head[76:], uint16(len(records)))
	off := len(head)
	for i, r := range records {
		be.PutUint32(head[78+i*8:], uint32(off))
		off += len(r)
	}
	out := head
	for _, r := range records {
		out = append(out, r...)
	}
	return out
}

func TestBookDropIngest_MOBI_IntakeIngestApprove(t *testing.T) {
	cases := []struct {
		name        string
		file        string
		fileVersion uint32
		wantFormat  string
	}{
		{name: "mobi", file: "dune.mobi", fileVersion: 6, wantFormat: "MOBI"},
		{name: "azw3", file: "dune.azw3", fileVersion: 8, wantFormat: "AZW3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			p := newBookDropPipeline(t)
			path := p.stage(t, c.file, mobiFixture("Dune", "Frank Herbert", c.fileVersion))

			// --- intake: the watcher's path -----------------------------------
			item, created, err := p.svc.Intake(ctx, path)
			if err != nil {
				t.Fatalf("Intake: %v", err)
			}
			if !created {
				t.Fatal("Intake: created = false, want true")
			}
			if item.Format != c.wantFormat {
				t.Fatalf("Intake: item.Format = %q, want %s", item.Format, c.wantFormat)
			}

			// --- ingest: the real fileproc.Dispatch + ExtractBook pass --------
			// Before #311 this failed the item with fileproc.NoProcessorError;
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

			// --- approve: placement + the books row ---------------------------
			book, err := p.svc.Approve(ctx, item.ID, p.lib.ID)
			if err != nil {
				t.Fatalf("Approve: %v", err)
			}
			if book.Format != c.wantFormat {
				t.Errorf("book.Format = %q, want %s", book.Format, c.wantFormat)
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

			wantLocation := filepath.Join("Frank Herbert", "Dune", c.file)
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
		})
	}
}

// A malformed .mobi fails the item with the processor's message rather
// than panicking the ingest worker or landing a half-empty book on the
// shelf. The bytes here are a PalmDB header whose record index claims
// records the file does not contain.
func TestBookDropIngest_MOBI_MalformedFailsTheItem(t *testing.T) {
	ctx := context.Background()
	p := newBookDropPipeline(t)

	raw := mobiFixture("Dune", "Frank Herbert", 6)
	binary.BigEndian.PutUint16(raw[76:], 60000) // a record count the file cannot hold
	path := p.stage(t, "broken.mobi", raw)

	item, _, err := p.svc.Intake(ctx, path)
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}

	// The worker call must return — with or without an error — rather than
	// panic; the item's own state is where the failure is recorded.
	_ = BookDropIngest(ctx, jobs.BookDropIngestArgs{ItemID: item.ID}, BookDropDeps{
		Svc:      p.svc,
		Resolver: p.resolver,
	})

	failed, err := p.svc.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get after ingest: %v", err)
	}
	if failed.State != model.BookDropFailed {
		t.Fatalf("state = %q, want error for a malformed file", failed.State)
	}
	if failed.ErrorMsg == "" {
		t.Error("expected the failure message to say what was wrong")
	}
}
