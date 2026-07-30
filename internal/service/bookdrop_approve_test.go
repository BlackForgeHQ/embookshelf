// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// --- fakes -------------------------------------------------------------

// fakeAutoEnrichPolicy stands in for the app_settings row Approve reads
// to decide whether Auto-enrich runs.
type fakeAutoEnrichPolicy struct {
	on   bool
	err  error
	keys []string
}

func (f *fakeAutoEnrichPolicy) GetBool(_ context.Context, key string) (bool, error) {
	f.keys = append(f.keys, key)
	if f.err != nil {
		return false, f.err
	}
	return f.on, nil
}

// fakeEnrichDispatcher records the book ids Approve handed to the worker
// pool. It never runs a provider fan-out — that is the point: the
// request goroutine only enqueues.
type fakeEnrichDispatcher struct {
	mu  sync.Mutex
	ids []string
	err error
}

func (f *fakeEnrichDispatcher) Enqueue(_ context.Context, a jobs.Args) error {
	args, ok := a.(jobs.BookDropAutoEnrichArgs)
	if !ok {
		return fmt.Errorf("unexpected job args %T", a)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.ids = append(f.ids, args.BookID)
	return nil
}

func (f *fakeEnrichDispatcher) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.ids))
	copy(out, f.ids)
	return out
}

// stubPlacer materialises nothing: Approve only needs the PlaceResult to
// build the books/files rows, and the placement adapters have their own
// tests.
type stubPlacer struct{ location string }

func (p stubPlacer) Place(context.Context, PlaceSource) (PlaceResult, error) {
	return PlaceResult{
		Location:   p.location,
		FolderPath: filepath.Dir(p.location),
		Size:       1024,
		Mtime:      time.Now(),
	}, nil
}

// --- harness -----------------------------------------------------------

type approveHarness struct {
	svc      *BookDropService
	item     model.BookDropItem
	library  model.Library
	policy   *fakeAutoEnrichPolicy
	dispatch *fakeEnrichDispatcher
}

// newApproveHarness builds a BookDropService over a migrated test schema
// with a ready bookdrop item and a library to approve it into. Covers,
// hub and the files repo are left nil — Approve guards each, and none of
// them participates in the Auto-enrich decision.
func newApproveHarness(t *testing.T, policy *fakeAutoEnrichPolicy) *approveHarness {
	t.Helper()
	ctx := t.Context()

	d := repotest.New(t)
	bdropRepo := repo.NewBookDropRepo(d)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)

	lib, err := libRepo.CreateLibrary(ctx, "Test Library", "test-library", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	item, err := bdropRepo.Insert(ctx, filepath.Join(t.TempDir(), "dune.epub"), "EPUB", 1024)
	if err != nil {
		t.Fatalf("Insert bookdrop item: %v", err)
	}
	if err := bdropRepo.SetMetadata(ctx, item.ID,
		"Dune", "Frank Herbert", "", "en", "", false, ""); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	dispatch := &fakeEnrichDispatcher{}
	svc := NewBookDropService(bdropRepo, libRepo, bookRepo, nil, nil, nil, dispatch).
		WithLibraryStore(&fakeLibStore{handle: &LibraryHandle{
			Library: lib,
			Placer:  stubPlacer{location: "Frank Herbert/Dune/dune.epub"},
		}}).
		WithAutoEnrichPolicy(policy)

	return &approveHarness{
		svc:      svc,
		item:     item,
		library:  lib,
		policy:   policy,
		dispatch: dispatch,
	}
}

// --- tests -------------------------------------------------------------

// Approve owns the Auto-enrich trigger: the enable setting is read here,
// not at the callsite, so every route into Approve gets the same policy.
func TestApproveRequestsAutoEnrichWhenTheSettingIsOn(t *testing.T) {
	h := newApproveHarness(t, &fakeAutoEnrichPolicy{on: true})

	book, err := h.svc.Approve(t.Context(), h.item.ID, h.library.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	got := h.dispatch.seen()
	if len(got) != 1 || got[0] != book.ID {
		t.Fatalf("dispatched %v, want [%s]", got, book.ID)
	}
	if len(h.policy.keys) != 1 || h.policy.keys[0] != repo.SettingMetadataAutoEnrich {
		t.Fatalf("policy read %v, want [%s]", h.policy.keys, repo.SettingMetadataAutoEnrich)
	}
}

// The setting is the whole decision — off means nothing is dispatched,
// and the import still succeeds.
func TestApproveSkipsAutoEnrichWhenTheSettingIsOff(t *testing.T) {
	h := newApproveHarness(t, &fakeAutoEnrichPolicy{on: false})

	if _, err := h.svc.Approve(t.Context(), h.item.ID, h.library.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := h.dispatch.seen(); len(got) != 0 {
		t.Fatalf("dispatched %v, want none", got)
	}
}

// A settings table that cannot be read degrades closed: enriching a book
// the admin may have opted out of is worse than not enriching one.
func TestApproveSkipsAutoEnrichWhenThePolicyReadFails(t *testing.T) {
	h := newApproveHarness(t, &fakeAutoEnrichPolicy{on: true, err: errors.New("settings unavailable")})

	if _, err := h.svc.Approve(t.Context(), h.item.ID, h.library.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := h.dispatch.seen(); len(got) != 0 {
		t.Fatalf("dispatched %v, want none", got)
	}
}

// Losing the job must not lose the import. The books row is already
// committed by the time the trigger runs; a queue that refuses only
// costs the gap-fill.
func TestApproveSucceedsWhenTheEnrichDispatchFails(t *testing.T) {
	h := newApproveHarness(t, &fakeAutoEnrichPolicy{on: true})
	h.dispatch.err = errors.New("queue down")

	book, err := h.svc.Approve(t.Context(), h.item.ID, h.library.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if book.ID == "" {
		t.Fatal("Approve returned no book")
	}
	item, err := h.svc.Get(t.Context(), h.item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.State != model.BookDropImported {
		t.Fatalf("item state = %q, want %q", item.State, model.BookDropImported)
	}
}

// With the queue unresolved the approve path still works — a binary
// without a worker pool imports books, it just never gap-fills them.
// jobs.Deferred refuses with ErrNoQueue until Resolve is called, and
// that refusal is exactly what requestAutoEnrich logs and swallows.
func TestApproveWithoutAnEnrichDispatcherStillImports(t *testing.T) {
	h := newApproveHarness(t, &fakeAutoEnrichPolicy{on: true})
	h.svc.enq = &jobs.Deferred{}

	if _, err := h.svc.Approve(t.Context(), h.item.ID, h.library.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := h.dispatch.seen(); len(got) != 0 {
		t.Fatalf("dispatched %v, want none", got)
	}
}

// Approve is the caller that places bytes, so the misplacement is stated
// end-to-end too: a real LibraryStore over a real DefaultPlacerBuilder,
// and a libraries row in the shape the storage-v2 backfill leaves — a
// filesystem library that also carries a kind=local backend row.
//
// The whole install's local backend is rooted at "/"
// (storageloader.buildBackend), so a library-relative key written
// through it lands outside the library; instanceRoot stands in for that
// "/" so the assertion doesn't need the machine's filesystem root (#265).
func TestApprovePlacesInsideAMigratedLocalLibrary(t *testing.T) {
	ctx := t.Context()
	d := repotest.New(t)

	instanceRoot := t.TempDir()
	libRoot := filepath.Join(instanceRoot, "srv", "books")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatalf("mkdir library root: %v", err)
	}

	libRepo := repo.NewLibraryRepo(d)
	lib, err := libRepo.CreateLibrary(ctx, "Migrated", "migrated", libRoot, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	// migrator.seedStorageBackends + wireLibraries: one kind=local backend
	// per distinct path, backend_id wired, root copied from path.
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

	// The install's LocalFS: one backend for the whole filesystem.
	fs, err := local.New(instanceRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	resolver := &storage.MapResolver{
		Default:  fs,
		Backends: map[string]storage.Storage{backend.ID: fs},
	}

	staging := t.TempDir()
	srcPath := filepath.Join(staging, "dune.epub")
	if err := os.WriteFile(srcPath, []byte("epub bytes"), 0o644); err != nil {
		t.Fatalf("write staged file: %v", err)
	}

	bdropRepo := repo.NewBookDropRepo(d)
	item, err := bdropRepo.Insert(ctx, srcPath, "EPUB", int64(len("epub bytes")))
	if err != nil {
		t.Fatalf("Insert bookdrop item: %v", err)
	}
	if err := bdropRepo.SetMetadata(ctx, item.ID,
		"Dune", "Frank Herbert", "", "en", "", false, ""); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	fileRepo := repo.NewFileRepo(d)
	svc := NewBookDropService(bdropRepo, libRepo, repo.NewBookRepo(d), nil, nil,
		fileRepo, &fakeEnrichDispatcher{}).
		WithLibraryStore(NewLibraryStore(LibraryStoreDeps{
			Libs:      libRepo,
			Resolver:  resolver,
			NewPlacer: DefaultPlacerBuilder(resolver),
			Files:     fileRepo,
		}))

	book, err := svc.Approve(ctx, item.ID, lib.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	wantLocation := filepath.Join("Frank Herbert", "Dune", "dune.epub")
	if book.Path != wantLocation {
		t.Errorf("book.Path=%q want the library-relative %q", book.Path, wantLocation)
	}
	if _, err := os.Stat(filepath.Join(libRoot, wantLocation)); err != nil {
		t.Errorf("approved bytes are not inside the library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(instanceRoot, wantLocation)); err == nil {
		t.Errorf("approved bytes landed at %q — the instance root, not the library",
			filepath.Join(instanceRoot, wantLocation))
	}

	files, err := fileRepo.ListByBook(ctx, book.ID)
	if err != nil {
		t.Fatalf("ListByBook: %v", err)
	}
	if len(files) != 1 || files[0].Location != wantLocation {
		t.Errorf("files rows = %+v, want one at %q", files, wantLocation)
	}
}
