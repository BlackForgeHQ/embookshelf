// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/sidecar"
)

// matchProvider is a stand-in metadata provider that always returns one
// canned match, so AutoEnrich can be exercised without network access.
type matchProvider struct {
	id      provider.Source
	matches []provider.Match
}

func (p *matchProvider) Name() provider.Source { return p.id }
func (p *matchProvider) Search(context.Context, provider.Query) ([]provider.Match, error) {
	return p.matches, nil
}

func applyHarness(t *testing.T) (*EnrichmentService, *fakeBookStore) {
	t.Helper()
	books := &fakeBookStore{}
	// The writer is the shipping ADR-0001 pipeline, not a stand-in: every
	// assertion below on books.updated now observes its DB step.
	writer, _ := newPipelineWriter(t, books, &recordingSidecarWriter{}, nil)
	return NewEnrichmentService(nil, newFakeProviderSettings(), books, &fakeCoverStore{}, writer), books
}

// sole returns the single book handed to the store, failing otherwise.
func sole(t *testing.T, books *fakeBookStore) model.Book {
	t.Helper()
	if len(books.updated) != 1 {
		t.Fatalf("writes = %d, want 1", len(books.updated))
	}
	return books.updated[0]
}

var fullMatch = provider.Match{
	Title:       "Provider Title",
	Authors:     []string{"Provider Author"},
	Description: "Provider description",
	Publisher:   "Provider Publisher",
	Series:      "Provider Series",
	Language:    "fr",
	Year:        1999,
	Categories:  []string{"Provider Genre"},
}

// --- the defect -------------------------------------------------------

// TestApplyMatchOnlyEmptyLeavesLocksAlone is the regression that motivated
// this change. AutoEnrich expressed its "empty fields only" policy by
// setting book.Locks true for every populated field and passing that book
// to the write step — which persists all 15 *_locked columns. The comment
// claimed the locks were reverted afterwards; nothing reverted them, so
// every auto-enriched book came out permanently locked on every field that
// already had a value.
func TestApplyMatchOnlyEmptyLeavesLocksAlone(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{
		ID:     "b1",
		Title:  "Local Title",
		Author: "Local Author",
		Genres: []string{"Local Genre"},
	}

	if _, _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{OnlyEmpty: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if got.Locks != (model.BookLocks{}) {
		t.Fatalf("locks were written: %+v — the policy must not touch them", got.Locks)
	}
}

// TestApplyMatchOnlyEmptyPreservesUserLocks — a lock the user set must
// still be honoured, and must survive the write unchanged.
func TestApplyMatchOnlyEmptyPreservesUserLocks(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{
		ID:    "b1",
		Locks: model.BookLocks{Publisher: true},
	}

	if _, _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{OnlyEmpty: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if !got.Locks.Publisher {
		t.Error("user lock on Publisher was dropped")
	}
	if got.Locks.Title || got.Locks.Author {
		t.Errorf("locks the user did not set were added: %+v", got.Locks)
	}
	if got.Publisher != "" {
		t.Errorf("Publisher = %q, want empty — the field was locked", got.Publisher)
	}
}

// --- the policy itself -------------------------------------------------

func TestApplyMatchOnlyEmptyFillsBlanksOnly(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{
		ID:     "b1",
		Title:  "Local Title",
		Author: "Local Author",
	}

	if _, _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{OnlyEmpty: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if got.Title != "Local Title" {
		t.Errorf("Title = %q, want the local value kept", got.Title)
	}
	if got.Author != "Local Author" {
		t.Errorf("Author = %q, want the local value kept", got.Author)
	}
	if got.Description != "Provider description" {
		t.Errorf("Description = %q, want the empty field filled", got.Description)
	}
	if got.Publisher != "Provider Publisher" {
		t.Errorf("Publisher = %q, want the empty field filled", got.Publisher)
	}
	if got.Language != "fr" {
		t.Errorf("Language = %q, want the empty field filled", got.Language)
	}
}

// TestApplyMatchOnlyEmptySkipsPopulatedSlices — a book that already has
// genres keeps them; the provider's list must not append or replace.
func TestApplyMatchOnlyEmptySkipsPopulatedSlices(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{ID: "b1", Genres: []string{"Local Genre"}}

	if _, _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{OnlyEmpty: true, MergeCategories: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if len(got.Genres) != 1 || got.Genres[0] != "Local Genre" {
		t.Fatalf("Genres = %#v, want the local list untouched", got.Genres)
	}
}

func TestApplyMatchOnlyEmptyFillsEmptySlices(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{ID: "b1"}

	if _, _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{OnlyEmpty: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if len(got.Genres) != 1 || got.Genres[0] != "Provider Genre" {
		t.Fatalf("Genres = %#v, want the provider list", got.Genres)
	}
}

// TestApplyMatchOnlyEmptyYearAndISBN — the numeric and routed-string
// fields need the same treatment as the plain strings.
func TestApplyMatchOnlyEmptyYearAndISBN(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{ID: "b1", Year: 1965, ISBN: "9780441013593"}

	if _, _, err := svc.ApplyMatch(context.Background(), book,
		provider.Match{Year: 1999, ISBN: "9999999999999"},
		ApplyOptions{OnlyEmpty: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if got.Year != 1965 {
		t.Errorf("Year = %d, want the local value kept", got.Year)
	}
	if got.ISBN != "9780441013593" {
		t.Errorf("ISBN = %q, want the local value kept", got.ISBN)
	}
}

// --- the interactive path is unchanged ---------------------------------

// TestApplyMatchWithoutOnlyEmptyOverwrites — the user picking a match in
// the UI still expects it to win over what is already stored.
func TestApplyMatchWithoutOnlyEmptyOverwrites(t *testing.T) {
	svc, store := applyHarness(t)
	book := model.Book{ID: "b1", Title: "Local Title", Author: "Local Author"}

	if _, _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{}, TriggerApplyEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if got.Title != "Provider Title" {
		t.Errorf("Title = %q, want the provider value", got.Title)
	}
	if got.Author != "Provider Author" {
		t.Errorf("Author = %q, want the provider value", got.Author)
	}
	if got.Locks != (model.BookLocks{}) {
		t.Errorf("locks written on the interactive path: %+v", got.Locks)
	}
}

// --- AutoEnrich: the defect site ---------------------------------------

// autoEnrichHarness wires a service whose single enabled provider always
// returns one high-confidence match, so AutoEnrich runs end to end.
func autoEnrichHarness(t *testing.T) (*EnrichmentService, *fakeBookStore) {
	t.Helper()
	books := &fakeBookStore{}
	settings := newFakeProviderSettings()
	settings.enabled["google_books"] = true
	p := &matchProvider{
		id:      provider.Source("google_books"),
		matches: []provider.Match{withConfidence(fullMatch, 95)},
	}
	writer, _ := newPipelineWriter(t, books, &recordingSidecarWriter{}, nil)
	svc := NewEnrichmentService(
		[]provider.Provider{p}, settings, books, &fakeCoverStore{}, writer,
	)
	return svc, books
}

func withConfidence(m provider.Match, c int) provider.Match {
	m.Confidence = c
	return m
}

// TestAutoEnrichNeverPersistsLocks pins the actual defect. AutoEnrich
// expressed ADR-0012's empty-only policy by setting book.Locks true for
// every populated field and handing that book to the write step, which
// persists all 15 *_locked columns. The comment claimed a revert that was
// never written, so every auto-enriched book came out permanently locked
// on every field it already had — locks the user never set and could only
// clear by hand, one book at a time.
func TestAutoEnrichNeverPersistsLocks(t *testing.T) {
	svc, books := autoEnrichHarness(t)
	book := model.Book{
		ID:          "b1",
		Title:       "Local Title",
		Author:      "Local Author",
		Description: "Local description",
		Publisher:   "Local Publisher",
		Series:      "Local Series",
		Language:    "en",
		Genres:      []string{"Local Genre"},
	}

	applied, err := svc.AutoEnrich(context.Background(), book)
	if err != nil {
		t.Fatalf("AutoEnrich: %v", err)
	}
	if !applied {
		t.Fatal("AutoEnrich reported no match despite a 95-confidence hit")
	}

	got := sole(t, books)
	if got.Locks != (model.BookLocks{}) {
		t.Fatalf("AutoEnrich persisted locks: %+v", got.Locks)
	}
}

// TestAutoEnrichKeepsPopulatedFields — the empty-only policy still holds
// now that it is carried by OnlyEmpty rather than a lock overlay.
func TestAutoEnrichKeepsPopulatedFields(t *testing.T) {
	svc, books := autoEnrichHarness(t)
	book := model.Book{
		ID:     "b1",
		Title:  "Local Title",
		Author: "Local Author",
	}

	if _, err := svc.AutoEnrich(context.Background(), book); err != nil {
		t.Fatalf("AutoEnrich: %v", err)
	}

	got := sole(t, books)
	if got.Title != "Local Title" {
		t.Errorf("Title = %q, want the local value kept", got.Title)
	}
	if got.Author != "Local Author" {
		t.Errorf("Author = %q, want the local value kept", got.Author)
	}
	if got.Publisher != "Provider Publisher" {
		t.Errorf("Publisher = %q, want the blank field filled", got.Publisher)
	}
}

// TestAutoEnrichHonoursExistingLocks — a lock the user set is still
// respected and still round-trips unchanged.
func TestAutoEnrichHonoursExistingLocks(t *testing.T) {
	svc, books := autoEnrichHarness(t)
	// Needs a title: Search short-circuits on a wholly empty query.
	book := model.Book{
		ID:    "b1",
		Title: "Local Title",
		Locks: model.BookLocks{Publisher: true},
	}

	if _, err := svc.AutoEnrich(context.Background(), book); err != nil {
		t.Fatalf("AutoEnrich: %v", err)
	}

	got := sole(t, books)
	if got.Publisher != "" {
		t.Errorf("Publisher = %q, want empty — the user locked it", got.Publisher)
	}
	if got.Locks != (model.BookLocks{Publisher: true}) {
		t.Fatalf("locks = %+v, want only the user's Publisher lock", got.Locks)
	}
}

// --- the shipping write sequence ---------------------------------------

// TestApplyMatchRunsTheWriteBackPipeline pins what the whole file could
// not reach while the MetadataWriter was an optional post-construction
// setter: with no test installing one, every ApplyMatch above fell into a
// direct-repo fallback that stopped at the books row, and the sequence
// ADR-0001 actually ships — DB → in-file embed → Sidecar → folder rename —
// was unreachable from this surface. Apply match is an explicit user
// action, so §3 puts the in-file write on it, and the Sidecar mode is
// decided from whether that write landed.
func TestApplyMatchRunsTheWriteBackPipeline(t *testing.T) {
	books := &fakeBookStore{}
	side := &recordingSidecarWriter{}
	emb := &fakeEmbedder{out: []byte("rezipped-epub-bytes")}
	writer, fs := newPipelineWriter(t, books, side,
		func(string) (fileproc.Embedder, error) { return emb, nil })
	ctx := context.Background()
	if _, err := fs.Put(ctx, "books/x.epub", strings.NewReader("original-bytes")); err != nil {
		t.Fatalf("seed book file: %v", err)
	}
	svc := NewEnrichmentService(nil, newFakeProviderSettings(), books, &fakeCoverStore{}, writer)

	book := model.Book{ID: "b1", LibraryID: "lib1", Path: "books/x.epub", Format: "EPUB"}
	if _, _, err := svc.ApplyMatch(ctx, book, fullMatch, ApplyOptions{}, TriggerApplyEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	// 1. DB — the canonical row.
	if got := sole(t, books); got.Title != "Provider Title" {
		t.Errorf("Title = %q, want the merged value persisted", got.Title)
	}
	// 2. In-file embed — carries the merged metadata, not the stored one.
	if len(emb.embeddedFor) != 1 {
		t.Fatalf("Embed called %d times, want 1 — the in-file step did not run", len(emb.embeddedFor))
	}
	if emb.embeddedFor[0] != "Provider Title" {
		t.Errorf("embedded title = %q, want the merged value", emb.embeddedFor[0])
	}
	rc, err := fs.Get(ctx, "books/x.epub")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	onDisk, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(onDisk) != "rezipped-epub-bytes" {
		t.Errorf("file bytes = %q, want the rewritten file", onDisk)
	}
	// 3. Sidecar — spillover, which is only chosen because the embed
	//    landed first. A broken embed step flips this to a full mirror.
	if len(side.calls) != 1 {
		t.Fatalf("Sidecar.Write called %d times, want 1", len(side.calls))
	}
	if side.calls[0].Mode != sidecar.ModeSpillover {
		t.Errorf("sidecar mode = %q, want spillover — the in-file write succeeded",
			side.calls[0].Mode)
	}
}
