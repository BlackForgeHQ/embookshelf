// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// EnrichmentService fans metadata queries across providers concurrently and
// merges the results. It also handles downloading + storing a cover image
// from a provider URL.
//
// The provider set passed at construction is the full list of built-in
// sources. Which of those are actually queried per request, and in what
// order, is decided by activeProviders from the provider_settings table —
// an admin toggles or reranks them in Settings and the next Search,
// SearchStream or LookupByISBN picks the change up. All three go through
// that one resolver; none reads the enabled flag or priority itself.

// The narrow interfaces below are the slices of each repo this service
// actually uses. They exist so the enrichment logic can be tested without
// a database or a filesystem — the same pattern that took Provisioner
// from untestable to 17 DB-free tests. Concrete repos satisfy them
// implicitly, so wiring is unchanged.

// providerRunStore is the run-time half of provider_settings: the ranked
// rows that decide which providers run and in what order, and the health
// counters each run writes back.
//
// The admin half — config blobs and the enabled/priority writes — is a
// separate interface next to its only consumer, ProviderSettingsService.
// The two services were split at #166; this seam followed at #250, once
// it was clear the halves are disjoint apart from List, which both need
// for different reasons: selection reads the rows to order them, the
// admin surface reads them to render them.
//
// EnabledIDs is gone rather than moved. Selection reads List because
// priority is part of the answer and only the full row carries it, which
// left the map-shaped helper with no caller on either side.
type providerRunStore interface {
	List(ctx context.Context) ([]repo.ProviderSetting, error)
	RecordSuccess(ctx context.Context, id string) error
	RecordError(ctx context.Context, id, msg string) error
}

// bookMetadataStore is the cover surface enrichment writes through.
// Metadata itself no longer travels this way — it goes through the
// MetadataWriter — so UpdateMetadata is deliberately absent: a caller
// that reaches for it would be bypassing ADR-0001.
type bookMetadataStore interface {
	SetCover(ctx context.Context, bookID string, hasCover bool, mime string) error
	SetCoverHash(ctx context.Context, bookID string, hash []byte) error
}

// coverFileStore is the cover-bytes surface.
type coverFileStore interface {
	SaveBookHashed(hash []byte, mime string, data []byte) error
	DeleteBook(id string) error
}

// httpDoer is the outbound HTTP seam for cover downloads. Injected so
// tests can serve bytes from a stub transport instead of the network.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type EnrichmentService struct {
	providers []provider.Provider
	settings  providerRunStore
	books     bookMetadataStore
	covers    coverFileStore
	http      httpDoer

	// No Cipher here. This service reads provider_settings but never
	// writes config, and the rows it reads arrive decrypted from the
	// repo — the field this struct used to carry was assigned at
	// construction and never read, a dead ADR-0010 obligation.

	// Result cache keyed by normalized (title|author|isbn). A fresh hit
	// returns without any upstream calls, which matters for Google Books
	// where the public quota is ~100 req / 100s per IP — admins tabbing
	// between books or re-opening the enrichment panel burn through it
	// fast without this.
	cacheMu  sync.Mutex
	cache    map[string]cacheEntry
	cacheTTL time.Duration

	// writer routes metadata writes through the ADR-0001 pipeline
	// (DB → in-file embed → sidecar → folder rename). Required.
	writer *MetadataWriter

	// healthWrites tracks the detached provider-health goroutines so a
	// test can wait for them. Production never waits — the whole point of
	// those writes is that a request doesn't block on telemetry — but
	// without this a test asserting on health races them.
	healthWrites sync.WaitGroup
}

// awaitHealthWrites blocks until every in-flight provider-health write has
// finished. Test-only; production deliberately lets them run detached.
func (s *EnrichmentService) awaitHealthWrites() { s.healthWrites.Wait() }

type cacheEntry struct {
	matches []provider.Match
	at      time.Time
}

const (
	enrichCacheTTL = 5 * time.Minute
)

// NewEnrichmentService builds the service. The MetadataWriter is a
// positional argument, not an optional setter: applying a match is an
// edit like any other, so there is no configuration in which it should
// skip the ADR-0001 pipeline. It used to be installed post-construction
// by WithMetadataWriter, which only the composition root ever called —
// every test therefore drove a direct-repo fallback that never ran in
// production, which is where ADR-0012's lock-corruption bug lived.
func NewEnrichmentService(
	providers []provider.Provider,
	settings providerRunStore,
	books bookMetadataStore,
	covers coverFileStore,
	writer *MetadataWriter,
) *EnrichmentService {
	return &EnrichmentService{
		providers: providers,
		settings:  settings,
		books:     books,
		covers:    covers,
		writer:    writer,
		http: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: coverRedirectPolicy(),
		},
		cache:    make(map[string]cacheEntry),
		cacheTTL: enrichCacheTTL,
	}
}

// WithHTTPClient swaps the outbound client used for cover downloads.
// Returns the receiver so it can be chained at construction.
func (s *EnrichmentService) WithHTTPClient(c httpDoer) *EnrichmentService {
	if c != nil {
		s.http = c
	}
	return s
}

// SearchResult bundles the fan-out output. QueriedProviders is the ID
// list of providers that actually ran this request — useful for the UI
// to explain an empty result set ("we searched X, Y, Z; no matches") or
// flag a fully-disabled setup.
type SearchResult struct {
	Matches          []provider.Match
	QueriedProviders []provider.Source
}

// activeProviders answers "which providers may run right now, in what
// order" for every external entry point: Search, SearchStream and
// LookupByISBN. It is the only place that reads the enabled flag or the
// priority column — the three used to walk provider_settings themselves
// and disagreed about the answer.
//
// Degrade closed (ADR-0013 §4). If the settings table cannot be read we
// return the error rather than guessing: an admin disables a provider
// deliberately — the Amazon and Goodreads adapters are scrapers, others
// cost quota — so querying one on a transient read failure sends traffic
// somewhere the operator explicitly refused. provider_settings shares a
// database with the book the request has already loaded, so "settings
// unreadable, rest of the request fine" is a narrow window.
//
// List rather than a bare enabled-set read because order is part of the
// answer: priority lives only on the full row. The extra columns cost one
// config decrypt per row, which is cheaper than two entry points
// disagreeing.
func (s *EnrichmentService) activeProviders(ctx context.Context) ([]provider.Provider, error) {
	rows, err := s.settings.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider settings: %w", err)
	}
	return selectProviders(s.providers, rows), nil
}

// selectProviders is activeProviders' pure core: filter the catalog by
// the enabled flag, then order by admin priority.
//
// A provider with no row is disabled, so nil, empty and all-false row
// sets are the same answer — nothing runs. That equivalence is the bug
// this function replaced: LookupByISBN gated its filter on `rows != nil`
// and so ran every provider on an empty table, Search's fan-out gated on
// a nil map and did the same, while SearchStream and Search's own
// reported provider list read the identical state as "nothing enabled".
//
// Ordering is a stable sort by priority ASC with ranked providers first;
// unranked ones keep catalog order behind them (ADR-0011 §2). Only
// LookupByISBN's serial chain is order-sensitive, but the fan-out paths
// report the same order so the UI's "we searched X, Y, Z" caption matches
// the chain an admin sees in Settings.
func selectProviders(all []provider.Provider, rows []repo.ProviderSetting) []provider.Provider {
	enabled := make(map[string]bool, len(rows))
	priority := make(map[string]int, len(rows))
	for _, r := range rows {
		enabled[r.ID] = r.Enabled
		if r.Priority != nil {
			priority[r.ID] = *r.Priority
		}
	}

	active := make([]provider.Provider, 0, len(all))
	for _, p := range all {
		if !enabled[string(p.Name())] {
			continue
		}
		active = append(active, p)
	}
	sort.SliceStable(active, func(i, j int) bool {
		pi, hasI := priority[string(active[i].Name())]
		pj, hasJ := priority[string(active[j].Name())]
		if hasI && hasJ {
			return pi < pj
		}
		return hasI && !hasJ
	})
	return active
}

// providerSources names a resolved provider set for the UI.
func providerSources(ps []provider.Provider) []provider.Source {
	out := make([]provider.Source, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}

// Search queries every enabled provider in parallel and returns a merged,
// sorted slice. A provider failure is logged but does not abort the
// fan-out. Disabled providers are skipped without hitting the network.
// Results are cached in-process for enrichCacheTTL so repeated UI opens
// with the same query don't re-hit upstream.
func (s *EnrichmentService) Search(ctx context.Context, q provider.Query) (SearchResult, error) {
	if len(s.providers) == 0 {
		return SearchResult{}, nil
	}
	// Short-circuit on empty query to avoid hitting the network with noise.
	if strings.TrimSpace(q.Title) == "" && strings.TrimSpace(q.Author) == "" && strings.TrimSpace(q.ISBN) == "" {
		return SearchResult{}, nil
	}

	// Who runs is activeProviders' call, including the degrade-closed
	// policy on an unreadable settings table (ADR-0013 §4). Naming the set
	// once is also what keeps QueriedProviders honest: the UI's "we
	// searched these three" caption is the same list the fan-out below
	// iterates, on cache hits and fresh runs alike.
	active, err := s.activeProviders(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	queried := providerSources(active)

	cacheKey := enrichCacheKey(q)
	if hit, ok := s.cacheGet(cacheKey); ok {
		return SearchResult{Matches: hit, QueriedProviders: queried}, nil
	}

	g, gctx := errgroup.WithContext(ctx)
	var (
		mu  sync.Mutex
		all []provider.Match
	)
	for _, p := range active {
		p := p
		g.Go(func() error {
			matches, err := p.Search(gctx, q)
			if err != nil {
				slog.Warn("provider search failed", "provider", p.Name(), "err", err)
				s.recordProviderError(p.Name(), err)
				return nil
			}
			s.recordProviderSuccess(p.Name())
			mu.Lock()
			all = append(all, matches...)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // never returns an error — providers swallow theirs.

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Confidence > all[j].Confidence
	})

	// Cache even empty results — a repeated fan-out over a title none
	// of the providers know about is just as wasteful as a repeated hit.
	s.cachePut(cacheKey, all)
	return SearchResult{Matches: all, QueriedProviders: queried}, nil
}

// StreamEvent is one frame pushed over the SSE channel. Provider is the
// source that produced the match (or the one that failed); Match is nil
// on the summary frame emitted after every provider has reported. Err
// is populated on per-provider failures so the UI can surface "X failed"
// without blocking the rest of the stream.
type StreamEvent struct {
	Provider provider.Source
	Match    *provider.Match
	// Done is true on the final frame. The handler uses it to emit a
	// `done` SSE event and close the stream cleanly.
	Done bool
	// Queried is populated on the Done frame with the full list of
	// providers the service decided to query. Lets the UI render the
	// "searched X, Y, Z" caption consistently.
	Queried []provider.Source
	// Err is the provider's error (if any). Non-nil values arrive on
	// per-provider frames; the stream itself never errors — each failed
	// provider just produces zero Match frames plus an Err signal.
	Err error
}

// SearchStream runs the provider fan-out and pushes results as they
// arrive. The returned channel is closed after the Done frame is
// emitted. Cancel the context to abort in-flight HTTP calls. Disabled
// providers are skipped just like Search().
//
// Unlike Search(), streaming results are NOT cached — each call starts
// fresh. Batched Search() retains its cache for the non-streaming
// fallback path.
func (s *EnrichmentService) SearchStream(ctx context.Context, q provider.Query) <-chan StreamEvent {
	out := make(chan StreamEvent, 8)
	if len(s.providers) == 0 ||
		(strings.TrimSpace(q.Title) == "" &&
			strings.TrimSpace(q.Author) == "" &&
			strings.TrimSpace(q.ISBN) == "") {
		go func() {
			out <- StreamEvent{Done: true}
			close(out)
		}()
		return out
	}

	// Same resolver as Search, so identical settings always select an
	// identical provider set here. The stream has no error return, so the
	// degrade-closed failure rides an Err frame followed by Done — the
	// handler already renders Err as a provider-error event, and Done keeps
	// the UI from waiting forever.
	active, err := s.activeProviders(ctx)
	if err != nil {
		go func() {
			out <- StreamEvent{Err: err}
			out <- StreamEvent{Done: true}
			close(out)
		}()
		return out
	}
	queried := providerSources(active)

	go func() {
		defer close(out)
		g, gctx := errgroup.WithContext(ctx)
		for _, p := range active {
			p := p
			g.Go(func() error {
				matches, err := p.Search(gctx, q)
				if err != nil {
					slog.Warn("provider search failed", "provider", p.Name(), "err", err)
					s.recordProviderError(p.Name(), err)
					select {
					case out <- StreamEvent{Provider: p.Name(), Err: err}:
					case <-gctx.Done():
						return gctx.Err()
					}
					return nil
				}
				s.recordProviderSuccess(p.Name())
				for i := range matches {
					m := matches[i]
					select {
					case out <- StreamEvent{Provider: p.Name(), Match: &m}:
					case <-gctx.Done():
						return gctx.Err()
					}
				}
				return nil
			})
		}
		_ = g.Wait()
		// Final frame carries the provider set so the UI can render
		// "we searched X, Y, Z" regardless of how many matches arrived.
		select {
		case out <- StreamEvent{Done: true, Queried: queried}:
		case <-ctx.Done():
		}
	}()

	return out
}

// LookupByISBN walks the active providers in priority order and returns
// the first non-empty hit. Used by the /isbn-lookup endpoint and the
// bookdrop auto-enrich path. The order is activeProviders' — ranked
// providers first, unranked in catalog order behind them (ADR-0011).
func (s *EnrichmentService) LookupByISBN(ctx context.Context, isbn string) (*provider.Match, provider.Source, error) {
	isbn = strings.TrimSpace(isbn)
	if isbn == "" {
		return nil, "", nil
	}
	// Same resolver as the two fan-out paths, which is what keeps the
	// enabled set and the priority order from drifting between them. It
	// also carries the degrade-closed policy (ADR-0013 §4).
	active, err := s.activeProviders(ctx)
	if err != nil {
		return nil, "", err
	}

	q := provider.Query{ISBN: isbn}
	for _, p := range active {
		matches, err := p.Search(ctx, q)
		if err != nil {
			slog.Warn("isbn lookup provider failed", "provider", p.Name(), "err", err)
			s.recordProviderError(p.Name(), err)
			continue
		}
		s.recordProviderSuccess(p.Name())
		if len(matches) == 0 {
			continue
		}
		best := matches[0]
		for i := range matches[1:] {
			if matches[i+1].Confidence > best.Confidence {
				best = matches[i+1]
			}
		}
		return &best, p.Name(), nil
	}
	return nil, "", nil
}

// ApplyOptions controls how a provider candidate is merged onto the
// stored book row. Locked fields on the book are always preserved
// regardless of what the candidate carries.
type ApplyOptions struct {
	// MergeCategories unions the candidate's categories into the
	// existing genres/tags instead of replacing them. With false
	// (the default), empty candidate slices leave the stored list alone
	// and non-empty ones overwrite wholesale.
	MergeCategories bool
	// ApplyCover pulls the candidate's cover URL if non-empty and the
	// cover lock is off. Failures are logged and swallowed so a broken
	// cover doesn't abort the metadata write.
	ApplyCover bool
	// OnlyEmpty fills blank fields and leaves populated ones alone —
	// ADR-0012's auto-enrich policy. It is a property of this apply, not
	// of the book: it must never reach the stored *_locked columns.
	//
	// The policy used to be emulated by setting book.Locks true for every
	// populated field, which the write step then persisted, permanently
	// locking every auto-enriched book on every field it already had.
	OnlyEmpty bool
}

// ApplyMatch merges the provider candidate onto the book and persists
// the result. Locked fields are preserved; categories merge or replace
// depending on options. Returns the refreshed book.
//
// The error is the write's, with a manual edit's meaning: nil says the
// whole plan landed, a *Degraded says the books row landed and a copy on
// disk did not. A degradation is not a reason to stop — the edit is
// persisted, so the cover import still runs and the book still comes
// back — but it does travel out to the caller, who is the only one able
// to tell the user their applied match did not reach the file.
//
// The cover import, when requested, is best-effort: failure is logged
// and the response still returns 200 with the metadata update applied.
// That matches how the UI already handles "Use cover" — users can retry
// independently.
func (s *EnrichmentService) ApplyMatch(ctx context.Context, book model.Book, m provider.Match, opts ApplyOptions, trigger Trigger) (model.Book, error) {
	locks := book.Locks

	// writable reports whether this apply may touch a field: never when
	// the user locked it, and under OnlyEmpty only when it is still blank.
	// The lock is named by its model.LockField constant rather than by
	// reaching into a BookLocks field, so a lock that gains a wire name
	// but no writability check here is visible to the parity test in
	// enrichment_locks_test.go instead of silently unprotected.
	writable := func(f model.LockField, populated bool) bool {
		return !locks.Get(f) && (!opts.OnlyEmpty || !populated)
	}

	if writable(model.LockTitle, book.Title != "") && m.Title != "" {
		book.Title = strings.TrimSpace(m.Title)
	}
	if writable(model.LockAuthor, book.Author != "") && len(m.Authors) > 0 {
		book.Author = strings.Join(m.Authors, ", ")
	}
	if writable(model.LockDescription, book.Description != "") && m.Description != "" {
		book.Description = m.Description
	}
	if writable(model.LockPublisher, book.Publisher != "") && m.Publisher != "" {
		book.Publisher = m.Publisher
	}
	if writable(model.LockSeries, book.Series != "") && m.Series != "" {
		book.Series = m.Series
	}
	if writable(model.LockLanguage, book.Language != "") && m.Language != "" {
		book.Language = m.Language
	}
	if m.ISBN != "" {
		// Providers hand back a single "ISBN" slot; route 13-digit
		// values to ISBN-13, shorter to ISBN-10. Each destination
		// column is gated by its own lock so that locking one form
		// does not block updates to the other.
		trimmed := strings.TrimSpace(m.ISBN)
		digits := countDigits(trimmed)
		switch {
		case digits == 13 && writable(model.LockISBN, book.ISBN != ""):
			book.ISBN = trimmed
		case digits == 10 && writable(model.LockISBN10, book.ISBN10 != ""):
			book.ISBN10 = trimmed
		case digits != 13 && digits != 10 && writable(model.LockISBN, book.ISBN != ""):
			book.ISBN = trimmed
		}
	}
	if writable(model.LockPublishDate, book.Year != 0) && m.Year > 0 {
		book.Year = m.Year
	}

	if writable(model.LockGenres, len(book.Genres) > 0) {
		clean := cleanCategorySlice(m.Categories)
		if len(clean) > 0 {
			if opts.MergeCategories {
				book.Genres = mergeCategorySlices(book.Genres, clean)
			} else {
				book.Genres = clean
			}
		}
	}

	// Only a fatal write stops the apply. A degradation means the edit is
	// in the books row and the caller has a user to warn, not a reason to
	// skip the cover and hand back an empty book.
	_, writeErr := s.writer.Write(ctx, book, trigger)
	if _, fatal := Degradation(writeErr); fatal {
		return model.Book{}, writeErr
	}

	if opts.ApplyCover && writable(model.LockCover, book.HasCover) && strings.TrimSpace(m.CoverURL) != "" {
		if _, err := s.ImportCoverFromURL(ctx, book.ID, m.CoverURL); err != nil {
			slog.Warn("apply match cover import", "book", book.ID, "err", err)
		}
	}

	return book, writeErr
}

// AutoEnrich is the headless variant of ApplyMatch used by the
// bookdrop auto-enrich path. It picks the highest-confidence match
// from the enabled providers and applies only to fields that are
// currently empty on the book — non-empty unlocked fields are
// preserved (the local EPUB/PDF extraction is usually trustworthy).
//
// Preference order:
//  1. ISBN chain — if book has an ISBN, walk LookupByISBN.
//  2. Fan-out via Search, pick the top result if Confidence >= 70.
//
// Failures are swallowed — auto-enrich is best-effort and must never
// block a successful import.
func (s *EnrichmentService) AutoEnrich(ctx context.Context, book model.Book) (bool, error) {
	var match *provider.Match
	var src provider.Source

	if strings.TrimSpace(book.ISBN) != "" {
		m, p, err := s.LookupByISBN(ctx, book.ISBN)
		if err != nil {
			slog.Warn("auto-enrich isbn lookup", "book", book.ID, "err", err)
		}
		match = m
		src = p
	}

	if match == nil {
		result, err := s.Search(ctx, provider.Query{
			Title:  book.Title,
			Author: book.Author,
			ISBN:   book.ISBN,
		})
		if err != nil {
			return false, err
		}
		if len(result.Matches) == 0 {
			return false, nil
		}
		// Apply only when the top hit is reasonably confident — the
		// scorer's 65 tier is "title contains"; 70 filters out pure
		// token-overlap noise.
		if result.Matches[0].Confidence < 70 {
			return false, nil
		}
		m := result.Matches[0]
		match = &m
	}
	if match == nil {
		return false, nil
	}
	slog.Info("auto-enrich applying", "book", book.ID, "source", src, "conf", match.Confidence)

	// The empty-only policy is an argument, not a mutation: OnlyEmpty
	// tells ApplyMatch to fill blanks and leave populated fields alone.
	// This used to be emulated by setting book.Locks true for every
	// populated field, which the write step persisted — permanently
	// locking every auto-enriched book on every field it already had,
	// against both the comment here and ADR-0012.
	//
	// Nothing is discarded here any more. Auto-enrichment is DB-only by
	// ADR-0001 §3, so there is no Sidecar or in-file step to degrade —
	// but that used to be an argument in a comment for throwing the whole
	// outcome away, and if the plan ever grew a step the comment would
	// have gone on being wrong quietly. Now a degradation is an error and
	// rides the return this path already has.
	_, applyErr := s.ApplyMatch(ctx, book, *match, ApplyOptions{
		MergeCategories: true,
		OnlyEmpty:       true,
		ApplyCover:      !book.Locks.Cover && !book.HasCover,
	}, TriggerAutoEnrichment)
	if deg, fatal := Degradation(applyErr); fatal {
		return false, applyErr
	} else if deg != nil {
		// The books row landed; only a copy on disk did not. The caller
		// is a job, and returning this as an error would have River
		// retry an apply that already succeeded. Unreachable while
		// auto-enrichment stays DB-only (ADR-0001 §3) — which is exactly
		// why it must not be left to whoever adds the step that breaks
		// that.
		slog.Warn("auto-enrich degraded", "book", book.ID, "warnings", deg.Warnings())
	}
	return true, nil
}

func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// cleanCategorySlice trims and dedupes provider-supplied categories.
// Case-sensitive dedup; empty strings dropped.
func cleanCategorySlice(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// mergeCategorySlices unions new values into an existing list,
// preserving the existing order and appending fresh entries at the end.
func mergeCategorySlices(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]string, 0, len(existing)+len(incoming))
	for _, c := range existing {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	for _, c := range incoming {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// AllowedCoverHosts caps which remote hosts we'll fetch cover bytes from.
// Providers hand back URLs; we refuse anything else to keep this endpoint
// from becoming an open proxy. Add new hosts here only after confirming
// the provider is one we trust.
//
// Entries with a leading "." are treated as suffix matches (e.g.
// ".gr-assets.com" admits "i.gr-assets.com" and "s.gr-assets.com") —
// useful for providers that fan covers across rotating subdomain CDNs
// without making the allow-list a whack-a-mole exercise.
// coverHostRule narrows an allow-list entry. A zero rule admits any path
// on the host; a non-empty Prefix admits only paths beginning with it,
// which is what keeps a host shared by many tenants from being usable
// wholesale.
type coverHostRule struct {
	Prefix string
}

var AllowedCoverHosts = map[string]coverHostRule{
	// Google Books + OAuth-signed image servers.
	"books.google.com":            {},
	"books.googleusercontent.com": {},
	// Open Library.
	"covers.openlibrary.org": {},
	// Amazon / AWS CDN (Goodreads + Amazon itself serve here).
	"images-na.ssl-images-amazon.com": {},
	"m.media-amazon.com":              {},
	// DuckDuckGo (Wikipedia-sourced covers).
	"duckduckgo.com": {},
	// Hardcover — covers sit on their asset CDN or a fallback GCS bucket.
	"assets.hardcover.app": {},
	// TODO: confirm Hardcover's bucket and set Prefix (e.g. "/hardcover/"),
	// so this stops admitting every bucket on the shared GCS host. Left
	// host-only because the URL comes verbatim from Hardcover's API and no
	// sample exists in-repo to derive the path from; guessing would reject
	// real covers silently, since a rejected cover is logged and swallowed.
	// The redirect escape this entry used to open is closed independently by
	// coverRedirectPolicy, which re-validates every hop.
	"storage.googleapis.com": {},
	// Goodreads CDNs. The search-results page serves the thumbnail
	// from a handful of rotating subdomains under these roots.
	".gr-assets.com":       {},
	".photo.goodreads.com": {},
}

// hostRule resolves a host against the allow-list. An exact match wins
// first; otherwise any entry with a leading "." is treated as a suffix
// match against the host.
func hostRule(host string) (coverHostRule, bool) {
	if r, ok := AllowedCoverHosts[host]; ok {
		return r, true
	}
	for pattern, r := range AllowedCoverHosts {
		if strings.HasPrefix(pattern, ".") && strings.HasSuffix(host, pattern) {
			return r, true
		}
	}
	return coverHostRule{}, false
}

// coverURLAllowed reports whether a URL may be fetched as a cover: https
// only, host on the allow-list, and path under that entry's prefix.
//
// Applied to the URL the caller supplied *and*, via coverRedirectPolicy, to
// every redirect target. Validating only the first URL is what let an
// allow-listed host bounce the fetch anywhere it liked.
func coverURLAllowed(u *url.URL) bool {
	if u == nil || u.Scheme != "https" {
		return false
	}
	rule, ok := hostRule(u.Host)
	if !ok {
		return false
	}
	if rule.Prefix == "" {
		return true
	}
	// Clean first so "/../tenant/" cannot masquerade as the prefix.
	return strings.HasPrefix(path.Clean(u.Path)+"/", rule.Prefix)
}

// maxCoverRedirects caps the redirect chain. Amazon and Goodreads both
// redirect in normal operation, so refusing outright would break real
// covers; a handful of hops is ample and bounds the work one request can
// cause.
const maxCoverRedirects = 5

// coverRedirectPolicy returns an http.Client CheckRedirect that holds the
// allow-list across the whole chain rather than just the first URL.
func coverRedirectPolicy() func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxCoverRedirects {
			return fmt.Errorf("%w: more than %d redirects", ErrBadCoverURL, maxCoverRedirects)
		}
		if !coverURLAllowed(req.URL) {
			return fmt.Errorf("%w: redirect to %q", ErrBadCoverURL, req.URL.Redacted())
		}
		return nil
	}
}

// ErrBadCoverURL is returned when the URL fails the host/scheme/content checks.
// Callers may wrap with %w — errors.Is(err, ErrBadCoverURL) distinguishes
// rejection from transient network failures.
var ErrBadCoverURL = errors.New("cover URL not allowed")

// maxCoverBytes is a safety cap for the cover download (10 MB is very generous
// for a book cover).
const maxCoverBytes = 10 * 1024 * 1024

// ImportCoverFromURL fetches an image from a vetted provider URL, stores it
// in the coverstore as the book's cover, and flips the DB flags. Returns the
// resolved MIME so the caller can surface it in a fragment response.
func (s *EnrichmentService) ImportCoverFromURL(ctx context.Context, bookID, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		slog.Warn("cover URL rejected: unparseable", "book", bookID, "url", rawURL, "err", err)
		return "", ErrBadCoverURL
	}
	if !coverURLAllowed(u) {
		// Surface the host in the error so admins can add it to the
		// allow-list after auditing. Logging too so the offending URL
		// is findable in server logs without a repro.
		slog.Warn("cover URL rejected: not allow-listed",
			"book", bookID, "host", u.Host, "url", rawURL)
		return "", fmt.Errorf("%w: %q not in allow-list", ErrBadCoverURL, u.Host)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("cover fetch non-200")
	}

	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		return "", errors.New("not an image")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxCoverBytes {
		return "", errors.New("cover too large")
	}

	// The type stored on the row and served back by the cover route comes
	// from the bytes, not from the header the upstream sent: an
	// allow-listed host is trusted to be a cover provider, not to be
	// honest about what it just returned (#330). The header check above
	// stays as an early-out on a plainly wrong response.
	mime := fileproc.SniffImageMime(body)
	if mime == "" {
		return "", errors.New("not an image")
	}

	sum := sha256.Sum256(body)
	if err := s.covers.SaveBookHashed(sum[:], mime, body); err != nil {
		return "", err
	}
	if err := s.books.SetCover(ctx, bookID, true, mime); err != nil {
		return "", err
	}
	if err := s.books.SetCoverHash(ctx, bookID, sum[:]); err != nil {
		return "", err
	}
	// Remove legacy id-keyed file so the BookCover handler's fallback
	// path doesn't serve a stale image if the hashed read ever errors.
	if err := s.covers.DeleteBook(bookID); err != nil {
		slog.Warn("cover import: delete legacy", "book", bookID, "err", err)
	}
	return mime, nil
}

// ClearCover removes the cover for a book: flips has_cover off, clears
// cover_mime + cover_hash, and best-effort deletes the legacy id-keyed
// cover file. Hashed cover bytes are content-addressed and may be shared
// with other books, so we intentionally don't touch the hashed file.
func (s *EnrichmentService) ClearCover(ctx context.Context, bookID string) error {
	if err := s.books.SetCover(ctx, bookID, false, ""); err != nil {
		return err
	}
	if err := s.books.SetCoverHash(ctx, bookID, nil); err != nil {
		return err
	}
	if err := s.covers.DeleteBook(bookID); err != nil {
		slog.Warn("cover clear: delete legacy", "book", bookID, "err", err)
	}
	return nil
}

// enrichCacheKey normalizes a query into a stable cache key. Whitespace
// is trimmed and the case folded so "bash cookbook" and "Bash Cookbook"
// share a cache entry. ISBN wins if present (it's the unique signal).
func enrichCacheKey(q provider.Query) string {
	isbn := strings.TrimSpace(q.ISBN)
	if isbn != "" {
		return "isbn|" + strings.ToLower(isbn)
	}
	t := strings.ToLower(strings.TrimSpace(q.Title))
	a := strings.ToLower(strings.TrimSpace(q.Author))
	return "q|" + t + "|" + a
}

func (s *EnrichmentService) cacheGet(key string) ([]provider.Match, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry, ok := s.cache[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.at) > s.cacheTTL {
		delete(s.cache, key)
		return nil, false
	}
	// Return a copy so callers that sort/mutate the slice don't corrupt
	// the cached entry for the next hit.
	out := make([]provider.Match, len(entry.matches))
	copy(out, entry.matches)
	return out, true
}

func (s *EnrichmentService) cachePut(key string, matches []provider.Match) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// Opportunistic cap so a long-running process with lots of unique
	// lookups doesn't drift unbounded. 512 entries ≈ a few hundred KB.
	if len(s.cache) > 512 {
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}
	stored := make([]provider.Match, len(matches))
	copy(stored, matches)
	s.cache[key] = cacheEntry{matches: stored, at: time.Now()}
}

// recordProviderSuccess and recordProviderError are fire-and-forget
// telemetry writes. They run on a detached context + goroutine so
// client disconnects don't cancel the health update, and a DB hiccup
// doesn't spill back into the request path.
func (s *EnrichmentService) recordProviderSuccess(id provider.Source) {
	s.healthWrites.Add(1)
	go func() {
		defer s.healthWrites.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.settings.RecordSuccess(ctx, string(id)); err != nil {
			slog.Warn("provider health write (success)", "provider", id, "err", err)
		}
	}()
}

func (s *EnrichmentService) recordProviderError(id provider.Source, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	s.healthWrites.Add(1)
	go func() {
		defer s.healthWrites.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.settings.RecordError(ctx, string(id), msg); err != nil {
			slog.Warn("provider health write (error)", "provider", id, "err", err)
		}
	}()
}
