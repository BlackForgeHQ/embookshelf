// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
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
