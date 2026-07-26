// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
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

func applyHarness() (*EnrichmentService, *fakeBookStore) {
	books := &fakeBookStore{}
	return NewEnrichmentService(nil, newFakeProviderSettings(), books, &fakeCoverStore{}, nil), books
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
	svc, store := applyHarness()
	book := model.Book{
		ID:     "b1",
		Title:  "Local Title",
		Author: "Local Author",
		Genres: []string{"Local Genre"},
	}

	if _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
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
	svc, store := applyHarness()
	book := model.Book{
		ID:    "b1",
		Locks: model.BookLocks{Publisher: true},
	}

	if _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
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
	svc, store := applyHarness()
	book := model.Book{
		ID:     "b1",
		Title:  "Local Title",
		Author: "Local Author",
	}

	if _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
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
	svc, store := applyHarness()
	book := model.Book{ID: "b1", Genres: []string{"Local Genre"}}

	if _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
		ApplyOptions{OnlyEmpty: true, MergeCategories: true}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("ApplyMatch: %v", err)
	}

	got := sole(t, store)
	if len(got.Genres) != 1 || got.Genres[0] != "Local Genre" {
		t.Fatalf("Genres = %#v, want the local list untouched", got.Genres)
	}
}

func TestApplyMatchOnlyEmptyFillsEmptySlices(t *testing.T) {
	svc, store := applyHarness()
	book := model.Book{ID: "b1"}

	if _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
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
	svc, store := applyHarness()
	book := model.Book{ID: "b1", Year: 1965, ISBN: "9780441013593"}

	if _, err := svc.ApplyMatch(context.Background(), book,
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
	svc, store := applyHarness()
	book := model.Book{ID: "b1", Title: "Local Title", Author: "Local Author"}

	if _, err := svc.ApplyMatch(context.Background(), book, fullMatch,
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
func autoEnrichHarness() (*EnrichmentService, *fakeBookStore) {
	books := &fakeBookStore{}
	settings := newFakeProviderSettings()
	settings.enabled["google_books"] = true
	p := &matchProvider{
		id:      provider.Source("google_books"),
		matches: []provider.Match{withConfidence(fullMatch, 95)},
	}
	svc := NewEnrichmentService(
		[]provider.Provider{p}, settings, books, &fakeCoverStore{}, nil,
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
	svc, books := autoEnrichHarness()
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
	svc, books := autoEnrichHarness()
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
	svc, books := autoEnrichHarness()
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
