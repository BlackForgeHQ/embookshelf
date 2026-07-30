// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ---------------------------------------------------------------------------
// Provider selection — the shared answer to "who runs, in what order"
// ---------------------------------------------------------------------------

// selectionProviders builds a service over three counting providers in
// catalog order, so a test can assert both which of them ran and in what
// order the entry point reported them.
func selectionProviders(t *testing.T, settings providerRunStore) (*EnrichmentService, []*countingProvider) {
	t.Helper()
	ps := []*countingProvider{
		{id: provider.Source("googlebooks")},
		{id: provider.Source("openlibrary")},
		{id: provider.Source("amazon")},
	}
	all := make([]provider.Provider, len(ps))
	for i, p := range ps {
		all[i] = p
	}
	svc := NewEnrichmentService(all, settings, &fakeBookStore{}, &fakeCoverStore{}, searchOnlyWriter(t))
	return svc, ps
}

// calledSources is the set of providers that actually saw a query, in
// catalog order — the ground truth an entry point's reported list has to
// agree with.
func calledSources(ps []*countingProvider) []provider.Source {
	out := make([]provider.Source, 0, len(ps))
	for _, p := range ps {
		if p.calls > 0 {
			out = append(out, p.id)
		}
	}
	return out
}

func sameSources(a, b []provider.Source) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSearchAndSearchStreamAgreeOnActiveProviders is the divergence this
// resolver exists to close. Both entry points read the same provider
// settings; a store that reports a nil enabled set — an empty table, or a
// repo handing back a nil map — must therefore produce the same active
// provider set on both. It did not: Search's fan-out loop skipped its
// enabled check entirely when the map was nil and queried every adapter,
// while SearchStream read nil as "nothing enabled" and queried none.
// Search also disagreed with itself, reporting a QueriedProviders list
// built with the opposite reading of nil from the one its fan-out used.
func TestSearchAndSearchStreamAgreeOnActiveProviders(t *testing.T) {
	t.Parallel()
	q := provider.Query{Title: "Dune"}

	// Same store state for both runs: enabled set comes back nil.
	batchSvc, batchProviders := selectionProviders(t, &fakeProviderSettings{})
	res, err := batchSvc.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	batchRan := calledSources(batchProviders)

	streamSvc, streamProviders := selectionProviders(t, &fakeProviderSettings{})
	var streamReported []provider.Source
	for ev := range streamSvc.SearchStream(context.Background(), q) {
		if ev.Done {
			streamReported = ev.Queried
		}
	}
	streamRan := calledSources(streamProviders)

	if !sameSources(batchRan, streamRan) {
		t.Errorf("same settings, different providers queried:\n  Search       %v\n  SearchStream %v",
			batchRan, streamRan)
	}
	if !sameSources(res.QueriedProviders, batchRan) {
		t.Errorf("Search reported %v but queried %v", res.QueriedProviders, batchRan)
	}
	if !sameSources(streamReported, streamRan) {
		t.Errorf("SearchStream reported %v but queried %v", streamReported, streamRan)
	}
}

// TestSelectProvidersResolvesEnabledSetAndOrder is the resolver's own
// contract: which providers run, and in what order. Nil, empty and
// all-disabled row sets all mean "nothing runs" — the equivalence the
// three entry points used to answer three different ways.
func TestSelectProvidersResolvesEnabledSetAndOrder(t *testing.T) {
	t.Parallel()

	catalog := []provider.Provider{
		&countingProvider{id: provider.Source("googlebooks")},
		&countingProvider{id: provider.Source("openlibrary")},
		&countingProvider{id: provider.Source("amazon")},
	}
	rank := func(n int) *int { return &n }
	on := func(id string, p *int) repo.ProviderSetting {
		return repo.ProviderSetting{ID: id, Enabled: true, Priority: p}
	}

	for _, tc := range []struct {
		name string
		rows []repo.ProviderSetting
		want []provider.Source
	}{
		{name: "nil rows run nothing", rows: nil},
		{name: "empty rows run nothing", rows: []repo.ProviderSetting{}},
		{
			name: "rows present but all disabled run nothing",
			rows: []repo.ProviderSetting{
				{ID: "googlebooks"}, {ID: "openlibrary"}, {ID: "amazon"},
			},
		},
		{
			name: "a provider with no row is disabled, not defaulted on",
			rows: []repo.ProviderSetting{on("openlibrary", nil)},
			want: []provider.Source{"openlibrary"},
		},
		{
			name: "unranked providers keep catalog order",
			rows: []repo.ProviderSetting{on("amazon", nil), on("googlebooks", nil)},
			want: []provider.Source{"googlebooks", "amazon"},
		},
		{
			name: "ranked providers sort ASC ahead of unranked",
			rows: []repo.ProviderSetting{
				on("googlebooks", nil), on("openlibrary", rank(9)), on("amazon", rank(1)),
			},
			want: []provider.Source{"amazon", "openlibrary", "googlebooks"},
		},
		{
			name: "row order does not affect the result",
			rows: []repo.ProviderSetting{
				on("amazon", rank(1)), on("openlibrary", rank(9)), on("googlebooks", nil),
			},
			want: []provider.Source{"amazon", "openlibrary", "googlebooks"},
		},
		{
			name: "a row for an unknown provider id is ignored",
			rows: []repo.ProviderSetting{on("hardcover", rank(1)), on("amazon", nil)},
			want: []provider.Source{"amazon"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := providerSources(selectProviders(catalog, tc.rows))
			if !sameSources(got, tc.want) {
				t.Errorf("active = %v, want %v", got, tc.want)
			}
		})
	}
}

// activeSettings is a store with the given providers on and, optionally,
// ranked. Rows come back in map order, so anything ordered downstream has
// to be the resolver's doing.
func activeSettings(enabled []string, priorities map[string]int) *fakeProviderSettings {
	s := newFakeProviderSettings()
	for _, id := range enabled {
		s.enabled[id] = true
	}
	s.priorities = priorities
	return s
}

// TestSearchQueriesOnlyResolvedProviders — the batch entry point honours
// the resolver: disabled adapters are never contacted, and the list it
// reports to the UI is the resolved order, on the cache hit as well as
// the fresh fan-out.
func TestSearchQueriesOnlyResolvedProviders(t *testing.T) {
	t.Parallel()
	settings := activeSettings(
		[]string{"googlebooks", "amazon"}, map[string]int{"amazon": 1})
	svc, ps := selectionProviders(t, settings)

	res, err := svc.Search(context.Background(), provider.Query{Title: "Dune"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []provider.Source{"amazon", "googlebooks"}
	if !sameSources(res.QueriedProviders, want) {
		t.Errorf("QueriedProviders = %v, want the resolved order %v", res.QueriedProviders, want)
	}
	if ran := calledSources(ps); !sameSources(ran, []provider.Source{"googlebooks", "amazon"}) {
		t.Errorf("queried %v, want only the enabled pair", ran)
	}
	if len(res.Matches) != 2 {
		t.Errorf("matches = %d, want one per enabled provider", len(res.Matches))
	}

	// The cached second call must report the same provider set — it is the
	// same question, and the UI caption should not change under it.
	cached, err := svc.Search(context.Background(), provider.Query{Title: "Dune"})
	if err != nil {
		t.Fatalf("Search (cached): %v", err)
	}
	if !sameSources(cached.QueriedProviders, want) {
		t.Errorf("cache hit reported %v, want %v", cached.QueriedProviders, want)
	}
	for _, p := range ps {
		if p.calls > 1 {
			t.Errorf("%s queried %d times — the cache hit re-ran the fan-out", p.id, p.calls)
		}
	}
}

// TestSearchStreamQueriesOnlyResolvedProviders — the streaming entry
// point honours the same resolver, per-frame and on the Done summary.
func TestSearchStreamQueriesOnlyResolvedProviders(t *testing.T) {
	t.Parallel()
	settings := activeSettings(
		[]string{"googlebooks", "amazon"}, map[string]int{"amazon": 1})
	svc, ps := selectionProviders(t, settings)

	var frames []provider.Source
	var reported []provider.Source
	for ev := range svc.SearchStream(context.Background(), provider.Query{Title: "Dune"}) {
		if ev.Done {
			reported = ev.Queried
			continue
		}
		frames = append(frames, ev.Provider)
	}

	want := []provider.Source{"amazon", "googlebooks"}
	if !sameSources(reported, want) {
		t.Errorf("Done.Queried = %v, want the resolved order %v", reported, want)
	}
	if ran := calledSources(ps); !sameSources(ran, []provider.Source{"googlebooks", "amazon"}) {
		t.Errorf("queried %v, want only the enabled pair", ran)
	}
	if len(frames) != 2 {
		t.Errorf("match frames = %d, want one per enabled provider", len(frames))
	}
	for _, f := range frames {
		if f == "openlibrary" {
			t.Error("a disabled provider produced a frame")
		}
	}
}

// TestLookupByISBNWalksResolvedProvidersInOrder — the chain honours the
// resolver too. It short-circuits on the first hit (ADR-0011 §1), so the
// winner is exactly the head of the resolved order: proof the chain reads
// the same enabled set and the same ranking as the fan-out paths.
func TestLookupByISBNWalksResolvedProvidersInOrder(t *testing.T) {
	t.Parallel()
	settings := activeSettings(
		[]string{"openlibrary", "amazon"},
		map[string]int{"amazon": 1, "openlibrary": 5},
	)
	svc, ps := selectionProviders(t, settings)

	match, src, err := svc.LookupByISBN(context.Background(), "9780441013593")
	if err != nil {
		t.Fatalf("LookupByISBN: %v", err)
	}
	if match == nil {
		t.Fatal("no match, want the top-ranked provider's hit")
	}
	if src != provider.Source("amazon") {
		t.Errorf("winner = %q, want amazon (priority 1)", src)
	}
	if ran := calledSources(ps); !sameSources(ran, []provider.Source{"amazon"}) {
		t.Errorf("queried %v, want the chain to stop at the first hit", ran)
	}
}
