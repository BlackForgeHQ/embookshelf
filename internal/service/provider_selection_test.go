// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/provider"
)

// countingProvider records whether it was queried, so a test can assert
// that a provider the admin disabled was never contacted.
type countingProvider struct {
	id    provider.Source
	calls int
}

func (p *countingProvider) Name() provider.Source { return p.id }
func (p *countingProvider) Search(context.Context, provider.Query) ([]provider.Match, error) {
	p.calls++
	return []provider.Match{{Title: "hit", Confidence: 90}}, nil
}

var errSettingsDown = errors.New("provider_settings unreadable")

func selectionHarness(t *testing.T, readErr error) (*EnrichmentService, *countingProvider) {
	t.Helper()
	p := &countingProvider{id: provider.Source("amazon")}
	settings := newFakeProviderSettings()
	settings.readErr = readErr
	svc := NewEnrichmentService(
		[]provider.Provider{p}, settings, &fakeBookStore{}, &fakeCoverStore{},
		searchOnlyWriter(t),
	)
	return svc, p
}

// searchOnlyWriter is the required MetadataWriter for tests that never
// write: selection is a read path, so the pipeline behind it is inert.
func searchOnlyWriter(t *testing.T) *MetadataWriter {
	t.Helper()
	w, _ := newPipelineWriter(t, &fakeBookStore{}, &recordingSidecarWriter{}, nil)
	return w
}

// The three search paths used to log "running all providers" and query
// everything when provider_settings could not be read. An admin disables a
// provider for a reason — the Amazon and Goodreads adapters are scrapers,
// and others cost quota — so overriding that on a transient database error
// sends queries somewhere the operator explicitly refused. The settings row
// lives in the same database as the book the request already loaded, so the
// failure this "degrade" covers barely occurs in isolation.

func TestSearchFailsWhenProviderSettingsUnreadable(t *testing.T) {
	svc, p := selectionHarness(t, errSettingsDown)

	_, err := svc.Search(context.Background(), provider.Query{Title: "Dune"})
	if !errors.Is(err, errSettingsDown) {
		t.Fatalf("err = %v, want the settings error surfaced", err)
	}
	if p.calls != 0 {
		t.Errorf("provider queried %d times despite unknown enabled set", p.calls)
	}
}

func TestLookupByISBNFailsWhenProviderSettingsUnreadable(t *testing.T) {
	svc, p := selectionHarness(t, errSettingsDown)

	_, _, err := svc.LookupByISBN(context.Background(), "9780441013593")
	if !errors.Is(err, errSettingsDown) {
		t.Fatalf("err = %v, want the settings error surfaced", err)
	}
	if p.calls != 0 {
		t.Errorf("provider queried %d times despite unknown enabled set", p.calls)
	}
}

func TestSearchStreamReportsUnreadableProviderSettings(t *testing.T) {
	svc, p := selectionHarness(t, errSettingsDown)

	var sawErr bool
	var done bool
	for ev := range svc.SearchStream(context.Background(), provider.Query{Title: "Dune"}) {
		if ev.Err != nil {
			sawErr = true
		}
		if ev.Done {
			done = true
		}
	}
	if !sawErr {
		t.Error("stream carried no error frame")
	}
	if !done {
		t.Error("stream never emitted Done — the UI would hang")
	}
	if p.calls != 0 {
		t.Errorf("provider queried %d times despite unknown enabled set", p.calls)
	}
}

// TestSelectionSkipsDisabledProvider is the ordinary path, pinned so the
// degrade change does not quietly invert it.
func TestSelectionSkipsDisabledProvider(t *testing.T) {
	svc, p := selectionHarness(t, nil)

	res, err := svc.Search(context.Background(), provider.Query{Title: "Dune"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("disabled provider was queried %d times", p.calls)
	}
	if len(res.Matches) != 0 {
		t.Errorf("matches = %d, want none", len(res.Matches))
	}
}

func TestSelectionQueriesEnabledProvider(t *testing.T) {
	p := &countingProvider{id: provider.Source("amazon")}
	settings := newFakeProviderSettings()
	settings.enabled["amazon"] = true
	svc := NewEnrichmentService(
		[]provider.Provider{p}, settings, &fakeBookStore{}, &fakeCoverStore{},
		searchOnlyWriter(t),
	)

	res, err := svc.Search(context.Background(), provider.Query{Title: "Dune"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(res.Matches))
	}
}

// TestLookupByISBNTreatsNoRowsAsNothingEnabled pins a consistency fix.
// LookupByISBN gated on `rows != nil`, so a nil slice — which the repo can
// return for an empty table — meant "no filter" and every provider ran,
// while Search's empty map meant "nothing enabled" and none did. Same
// database state, opposite behaviour.
func TestLookupByISBNTreatsNoRowsAsNothingEnabled(t *testing.T) {
	p := &countingProvider{id: provider.Source("amazon")}
	settings := newFakeProviderSettings() // no enabled entries → no rows
	svc := NewEnrichmentService(
		[]provider.Provider{p}, settings, &fakeBookStore{}, &fakeCoverStore{},
		searchOnlyWriter(t),
	)

	match, _, err := svc.LookupByISBN(context.Background(), "9780441013593")
	if err != nil {
		t.Fatalf("LookupByISBN: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("provider queried %d times with nothing enabled", p.calls)
	}
	if match != nil {
		t.Errorf("match = %+v, want none", match)
	}
}
