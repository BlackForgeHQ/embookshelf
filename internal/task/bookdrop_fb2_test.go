// SPDX-License-Identifier: AGPL-3.0-or-later

package task

// The BookDrop pipeline driven end to end for .fb2 (#312): Intake (the
// row a watcher or upload would create), BookDropIngest (the real
// fileproc.Dispatch + ExtractBook pass — the thing that used to answer
// "no processor for FB2 yet" before this issue wired one), and Approve
// (placement + the books row). A real Postgres schema throughout and a
// real local storage backend, following the shape
// TestApprovePlacesInsideAMigratedLocalLibrary already established for
// EPUB: this is the same harness, run with an .fb2 file so the
// processor is exercised by the pipeline that actually calls it in
// production, not just by fileproc's own unit tests.

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
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
	d := repotest.New(t)

	instanceRoot := t.TempDir()
	libRoot := filepath.Join(instanceRoot, "library")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatalf("mkdir library root: %v", err)
	}

	libRepo := repo.NewLibraryRepo(d)
	lib, err := libRepo.CreateLibrary(ctx, "Test Library", "test-library", libRoot, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	// migrator.seedStorageBackends + wireLibraries: one kind=local backend
	// per distinct path, wired onto the library — the shape a real boot
	// leaves, mirrored here rather than assumed.
	backend, err := repo.NewStorageBackendRepo(d).Create(ctx, "local",
		map[string]any{"root": libRoot})
	if err != nil {
		t.Fatalf("Create backend: %v", err)
	}
	if _, err := d.SQL.ExecContext(ctx,
		`UPDATE libraries SET backend_id = $1, root = path WHERE id = $2`,
		backend.ID, lib.ID,
	); err != nil {
		t.Fatalf("wire library to backend: %v", err)
	}

	// Every local backend is constructed rooted at "/" for the whole
	// instance (storageloader.buildBackend); the bookdrop item's path is
	// an absolute filesystem path outside the library root, and ingest's
	// resolver.Resolve("") needs to reach it the same way a real boot
	// would.
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	resolver := &storage.MapResolver{
		Default:  fs,
		Backends: map[string]storage.Storage{backend.ID: fs},
	}

	staging := t.TempDir()
	fb2Path := filepath.Join(staging, "dune.fb2")
	if err := os.WriteFile(fb2Path, fb2Fixture(), 0o644); err != nil {
		t.Fatalf("write staged fb2: %v", err)
	}

	bdropRepo := repo.NewBookDropRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)

	svc := service.NewBookDropService(bdropRepo, libRepo, bookRepo, nil, nil, fileRepo, &jobs.Deferred{}).
		WithLibraryStore(service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:      libRepo,
			Resolver:  resolver,
			NewPlacer: service.DefaultPlacerBuilder(resolver),
			Files:     fileRepo,
		})).
		WithBookDropPath(staging)

	// --- intake: the watcher's path -------------------------------------
	item, created, err := svc.Intake(ctx, fb2Path)
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
		Svc:      svc,
		Resolver: resolver,
	}); err != nil {
		t.Fatalf("BookDropIngest: %v", err)
	}

	ingested, err := svc.Get(ctx, item.ID)
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
	book, err := svc.Approve(ctx, item.ID, lib.ID)
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
	if _, err := os.Stat(filepath.Join(libRoot, wantLocation)); err != nil {
		t.Errorf("approved bytes are not inside the library: %v", err)
	}

	final, err := svc.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get after approve: %v", err)
	}
	if final.State != model.BookDropImported {
		t.Errorf("state = %q, want imported", final.State)
	}
}
