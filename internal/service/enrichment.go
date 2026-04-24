package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/crypto"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/repo"
)

// EnrichmentService fans metadata queries across providers concurrently and
// merges the results. It also handles downloading + storing a cover image
// from a provider URL.
//
// The provider set passed at construction is the full list of built-in
// sources. Which of those are actually queried per request is decided by
// the provider_settings table — an admin toggles them in Settings and the
// filter below applies on the next Search.
type EnrichmentService struct {
	providers []provider.Provider
	settings  *repo.ProviderSettingsRepo
	libs      *repo.LibraryRepo
	covers    *coverstore.Store
	http      *http.Client
	// cipher encrypts password-kind config fields (API keys, tokens,
	// cookies) before they land in provider_settings.config. Falls
	// back to a Noop in dev so the app still boots without a KEK.
	cipher crypto.Cipher

	// Result cache keyed by normalized (title|author|isbn). A fresh hit
	// returns without any upstream calls, which matters for Google Books
	// where the public quota is ~100 req / 100s per IP — admins tabbing
	// between books or re-opening the enrichment panel burn through it
	// fast without this.
	cacheMu  sync.Mutex
	cache    map[string]cacheEntry
	cacheTTL time.Duration
}

type cacheEntry struct {
	matches []provider.Match
	at      time.Time
}

const (
	enrichCacheTTL = 5 * time.Minute
)

// ErrUnknownProvider is returned by SetProviderEnabled when the caller
// hands in an id the binary doesn't recognize.
var ErrUnknownProvider = errors.New("unknown provider")

// ProviderInfo is the handler-facing DTO shape: static catalog facts
// joined with the live enabled flag, config blob, priority, health
// telemetry, and the admin-UI schema describing which inputs to render.
type ProviderInfo struct {
	ID       provider.Source
	Name     string
	Enabled  bool
	External bool
	Priority *int
	// Config is the stored blob, pass-through to the UI. Providers that
	// aren't Configurable always have nil here.
	Config []byte
	// Schema describes the form fields the Settings panel should render.
	// nil when the provider doesn't declare a schema.
	Schema []provider.ConfigField
	// Health — last-success / last-error timestamps and the most
	// recent error string. Nil timestamps mean "never observed."
	LastSuccessAt *time.Time
	LastErrorAt   *time.Time
	LastError     string
}

func NewEnrichmentService(
	providers []provider.Provider,
	settings *repo.ProviderSettingsRepo,
	libs *repo.LibraryRepo,
	covers *coverstore.Store,
	cipher crypto.Cipher,
) *EnrichmentService {
	if cipher == nil {
		cipher = crypto.Noop{}
	}
	return &EnrichmentService{
		providers:   providers,
		settings:    settings,
		libs:        libs,
		covers:      covers,
		http:        &http.Client{Timeout: 15 * time.Second},
		cipher:      cipher,
		cache:    make(map[string]cacheEntry),
		cacheTTL: enrichCacheTTL,
	}
}

// LoadConfigs pushes stored provider configs into the matching
// provider instances. Called on service boot. Failures are logged per
// provider — one broken blob shouldn't wedge the others.
//
// Password-kind fields are decrypted before being handed to the
// provider so the live Configure call always sees plaintext.
func (s *EnrichmentService) LoadConfigs(ctx context.Context) error {
	raw, err := s.settings.AllConfigs(ctx)
	if err != nil {
		return err
	}
	for _, p := range s.providers {
		cfg, ok := raw[string(p.Name())]
		if !ok {
			continue
		}
		configurable, isCfg := p.(provider.Configurable)
		if !isCfg {
			continue
		}
		decoded, err := s.decryptConfigFields(cfg, p)
		if err != nil {
			slog.Warn("provider config decrypt failed", "provider", p.Name(), "err", err)
			continue
		}
		if err := configurable.Configure(decoded); err != nil {
			slog.Warn("provider configure failed", "provider", p.Name(), "err", err)
		}
	}
	return nil
}

// passwordFields returns the set of config keys a provider flags as
// password-kind — i.e. values that should be encrypted on disk.
func passwordFields(p provider.Provider) map[string]struct{} {
	sp, ok := p.(provider.SchemaProvider)
	if !ok {
		return nil
	}
	out := map[string]struct{}{}
	for _, f := range sp.ConfigSchema() {
		if f.Kind == provider.ConfigFieldPassword {
			out[f.Key] = struct{}{}
		}
	}
	return out
}

// encryptConfigFields walks a config blob and encrypts the string
// values whose keys are declared password-kind in the provider's
// schema. Returns the re-marshaled blob; non-password keys pass
// through verbatim so the stored JSON stays inspectable.
func (s *EnrichmentService) encryptConfigFields(cfg []byte, p provider.Provider) ([]byte, error) {
	return s.transformConfigFields(cfg, p, s.cipher.Encrypt)
}

// decryptConfigFields is the inverse of encryptConfigFields. Applied
// after every DB read so callers see plaintext secrets.
func (s *EnrichmentService) decryptConfigFields(cfg []byte, p provider.Provider) ([]byte, error) {
	return s.transformConfigFields(cfg, p, s.cipher.Decrypt)
}

func (s *EnrichmentService) transformConfigFields(
	cfg []byte, p provider.Provider, op func(string) (string, error),
) ([]byte, error) {
	if len(cfg) == 0 || len(cfg) == 2 { // "{}"
		return cfg, nil
	}
	pw := passwordFields(p)
	if len(pw) == 0 {
		return cfg, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(cfg, &obj); err != nil {
		return nil, err
	}
	for key := range pw {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		str, ok := raw.(string)
		if !ok {
			continue
		}
		transformed, err := op(str)
		if err != nil {
			return nil, err
		}
		obj[key] = transformed
	}
	return json.Marshal(obj)
}

// SearchResult bundles the fan-out output. QueriedProviders is the ID
// list of providers that actually ran this request — useful for the UI
// to explain an empty result set ("we searched X, Y, Z; no matches") or
// flag a fully-disabled setup.
type SearchResult struct {
	Matches          []provider.Match
	QueriedProviders []provider.Source
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

	// Fetch the live enabled map; on DB error fall through to the full
	// provider list so an outage of the settings table doesn't silently
	// disable enrichment entirely. Best-effort graceful degrade.
	enabled, err := s.settings.EnabledIDs(ctx)
	if err != nil {
		slog.Warn("provider settings fetch — running all providers", "err", err)
		enabled = nil
	}

	// Compute the set we'd actually query so the UI can say "we searched
	// these three" consistently across cache hits and fresh fan-outs.
	queried := make([]provider.Source, 0, len(s.providers))
	for _, p := range s.providers {
		if enabled != nil && !enabled[string(p.Name())] {
			continue
		}
		queried = append(queried, p.Name())
	}

	cacheKey := enrichCacheKey(q)
	if hit, ok := s.cacheGet(cacheKey); ok {
		return SearchResult{Matches: hit, QueriedProviders: queried}, nil
	}

	g, gctx := errgroup.WithContext(ctx)
	var (
		mu  sync.Mutex
		all []provider.Match
	)
	for _, p := range s.providers {
		p := p
		if enabled != nil && !enabled[string(p.Name())] {
			continue
		}
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

	enabled, err := s.settings.EnabledIDs(ctx)
	if err != nil {
		slog.Warn("provider settings fetch — running all providers", "err", err)
		enabled = nil
	}

	queried := make([]provider.Source, 0, len(s.providers))
	type run struct{ p provider.Provider }
	runs := make([]run, 0, len(s.providers))
	for _, p := range s.providers {
		if enabled != nil && !enabled[string(p.Name())] {
			continue
		}
		queried = append(queried, p.Name())
		runs = append(runs, run{p: p})
	}

	go func() {
		defer close(out)
		g, gctx := errgroup.WithContext(ctx)
		for _, r := range runs {
			r := r
			g.Go(func() error {
				matches, err := r.p.Search(gctx, q)
				if err != nil {
					slog.Warn("provider search failed", "provider", r.p.Name(), "err", err)
					s.recordProviderError(r.p.Name(), err)
					select {
					case out <- StreamEvent{Provider: r.p.Name(), Err: err}:
					case <-gctx.Done():
						return gctx.Err()
					}
					return nil
				}
				s.recordProviderSuccess(r.p.Name())
				for i := range matches {
					m := matches[i]
					select {
					case out <- StreamEvent{Provider: r.p.Name(), Match: &m}:
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

// LookupByISBN walks enabled providers in priority order and returns
// the first non-empty hit. Used by the /isbn-lookup endpoint and the
// bookdrop auto-enrich path. Priority comes from provider_settings;
// unranked providers fall back to catalog order after ranked ones.
func (s *EnrichmentService) LookupByISBN(ctx context.Context, isbn string) (*provider.Match, provider.Source, error) {
	isbn = strings.TrimSpace(isbn)
	if isbn == "" {
		return nil, "", nil
	}
	rows, err := s.settings.List(ctx)
	if err != nil {
		slog.Warn("provider settings fetch — running all providers", "err", err)
		rows = nil
	}
	enabled := map[string]bool{}
	priority := map[string]int{}
	for _, r := range rows {
		enabled[r.ID] = r.Enabled
		if r.Priority != nil {
			priority[r.ID] = *r.Priority
		}
	}

	// Stable sort by priority ASC (ranked first), unranked fall back to
	// the existing catalog order already baked into s.providers.
	ordered := make([]provider.Provider, len(s.providers))
	copy(ordered, s.providers)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, hasI := priority[string(ordered[i].Name())]
		pj, hasJ := priority[string(ordered[j].Name())]
		if hasI && hasJ {
			return pi < pj
		}
		if hasI {
			return true
		}
		if hasJ {
			return false
		}
		return false
	})

	q := provider.Query{ISBN: isbn}
	for _, p := range ordered {
		if rows != nil && !enabled[string(p.Name())] {
			continue
		}
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

// ListProviders joins the static catalog with the live per-row state
// (enabled + config + priority) and the provider's declared schema.
// Missing rows count as disabled with an empty config. The returned
// slice is in catalog order; the handler re-sorts by priority for the
// admin UI if it wants chain order.
func (s *EnrichmentService) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	rows, err := s.settings.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]repo.ProviderSetting, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	byProvider := make(map[provider.Source]provider.Provider, len(s.providers))
	for _, p := range s.providers {
		byProvider[p.Name()] = p
	}
	out := make([]ProviderInfo, 0, len(provider.Catalog))
	for _, c := range provider.Catalog {
		info := ProviderInfo{
			ID:       c.ID,
			Name:     c.Name,
			External: c.External,
		}
		if row, ok := byID[string(c.ID)]; ok {
			info.Enabled = row.Enabled
			info.Priority = row.Priority
			info.Config = []byte(row.Config)
			info.LastSuccessAt = row.LastSuccessAt
			info.LastErrorAt = row.LastErrorAt
			info.LastError = row.LastError
		}
		if p, ok := byProvider[c.ID]; ok {
			if sp, ok := p.(provider.SchemaProvider); ok {
				info.Schema = sp.ConfigSchema()
			}
			// Decrypt password-kind fields for the admin UI. Failures
			// return the raw blob so an admin can at least see the row
			// exists (the input will be gibberish, which is a visible
			// signal the KEK rotated out of sync).
			if decoded, err := s.decryptConfigFields(info.Config, p); err != nil {
				slog.Warn("list providers config decrypt", "provider", c.ID, "err", err)
			} else {
				info.Config = decoded
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// SetProviderEnabled flips the enabled flag for a single provider. The
// id must match an entry in the static catalog; unknown ids return
// ErrUnknownProvider rather than silently upserting junk.
func (s *EnrichmentService) SetProviderEnabled(ctx context.Context, id string, enabled bool) error {
	if _, ok := provider.CatalogLookup(id); !ok {
		return ErrUnknownProvider
	}
	return s.settings.SetEnabled(ctx, id, enabled)
}

// SetProviderConfig stores a new config blob and pushes it into the
// running provider instance. Invalid JSON is caught by Configure and
// surfaced to the caller so a save button can flash an error.
//
// Password-kind fields (API keys, cookies) are encrypted before they
// land in the DB; the live provider gets the plaintext copy via
// Configure so the next outbound HTTP request works immediately.
func (s *EnrichmentService) SetProviderConfig(ctx context.Context, id string, cfg []byte) error {
	info, ok := provider.CatalogLookup(id)
	if !ok {
		return ErrUnknownProvider
	}
	var matched provider.Provider
	for _, p := range s.providers {
		if p.Name() == info.ID {
			matched = p
			break
		}
	}

	toStore := cfg
	if matched != nil {
		encrypted, err := s.encryptConfigFields(cfg, matched)
		if err != nil {
			return err
		}
		toStore = encrypted
	}
	if err := s.settings.SetConfig(ctx, id, toStore); err != nil {
		return err
	}
	if matched == nil {
		return nil
	}
	configurable, isCfg := matched.(provider.Configurable)
	if !isCfg {
		return nil
	}
	// The live Configure call reads the plaintext blob — decryption
	// round-tripping through the DB isn't required.
	return configurable.Configure(cfg)
}

// SetProviderPriority stores the sort priority. nil clears it.
func (s *EnrichmentService) SetProviderPriority(ctx context.Context, id string, priority *int) error {
	if _, ok := provider.CatalogLookup(id); !ok {
		return ErrUnknownProvider
	}
	return s.settings.SetPriority(ctx, id, priority)
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
}

// ApplyMatch merges the provider candidate onto the book and persists
// the result. Locked fields are preserved; categories merge or replace
// depending on options. Returns the refreshed book.
//
// The cover import, when requested, is best-effort: failure is logged
// and the response still returns 200 with the metadata update applied.
// That matches how the UI already handles "Use cover" — users can retry
// independently.
func (s *EnrichmentService) ApplyMatch(ctx context.Context, book model.Book, m provider.Match, opts ApplyOptions) (model.Book, error) {
	locks := book.Locks

	if !locks.Title && m.Title != "" {
		book.Title = strings.TrimSpace(m.Title)
	}
	if !locks.Author && len(m.Authors) > 0 {
		book.Author = strings.Join(m.Authors, ", ")
	}
	if !locks.Description && m.Description != "" {
		book.Description = m.Description
	}
	if !locks.Publisher && m.Publisher != "" {
		book.Publisher = m.Publisher
	}
	if !locks.Series && m.Series != "" {
		book.Series = m.Series
	}
	if !locks.Language && m.Language != "" {
		book.Language = m.Language
	}
	if !locks.ISBN && m.ISBN != "" {
		// Providers hand back a single "ISBN" slot; route 13-digit
		// values to ISBN-13, shorter to ISBN-10 so we don't smash the
		// wrong column when both lock flags are off.
		trimmed := strings.TrimSpace(m.ISBN)
		digits := countDigits(trimmed)
		switch {
		case digits == 13:
			book.ISBN = trimmed
		case digits == 10 && !locks.ISBN10:
			book.ISBN10 = trimmed
		default:
			book.ISBN = trimmed
		}
	}
	if !locks.PublishDate && m.Year > 0 {
		book.Year = m.Year
	}

	if !locks.Genres {
		clean := cleanCategorySlice(m.Categories)
		if len(clean) > 0 {
			if opts.MergeCategories {
				book.Genres = mergeCategorySlices(book.Genres, clean)
			} else {
				book.Genres = clean
			}
		}
	}

	if err := s.libs.UpdateMetadata(ctx, book); err != nil {
		return model.Book{}, err
	}

	if opts.ApplyCover && !locks.Cover && strings.TrimSpace(m.CoverURL) != "" {
		if _, err := s.ImportCoverFromURL(ctx, book.ID, m.CoverURL); err != nil {
			slog.Warn("apply match cover import", "book", book.ID, "err", err)
		}
	}

	return book, nil
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

	// Emulate the "empty only" policy by locking every currently-filled
	// field for the duration of this apply. We revert locks after so
	// admin-set locks stay in the DB.
	applyLocks := book.Locks
	if strings.TrimSpace(book.Title) != "" {
		applyLocks.Title = true
	}
	if strings.TrimSpace(book.Author) != "" {
		applyLocks.Author = true
	}
	if strings.TrimSpace(book.Description) != "" {
		applyLocks.Description = true
	}
	if strings.TrimSpace(book.Publisher) != "" {
		applyLocks.Publisher = true
	}
	if strings.TrimSpace(book.Series) != "" {
		applyLocks.Series = true
	}
	if strings.TrimSpace(book.Language) != "" {
		applyLocks.Language = true
	}
	if strings.TrimSpace(book.ISBN) != "" {
		applyLocks.ISBN = true
	}
	if len(book.Genres) > 0 {
		applyLocks.Genres = true
	}
	if book.HasCover {
		applyLocks.Cover = true
	}
	// Swap in the temporary locks only for the apply call.
	originalLocks := book.Locks
	book.Locks = applyLocks

	if _, err := s.ApplyMatch(ctx, book, *match, ApplyOptions{
		MergeCategories: true,
		ApplyCover:      !originalLocks.Cover && !book.HasCover,
	}); err != nil {
		return false, err
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
var AllowedCoverHosts = map[string]struct{}{
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
	// Hardcover — covers sit on their asset CDN or fallback GCS bucket.
	"assets.hardcover.app":   {},
	"storage.googleapis.com": {},
	// Goodreads CDNs. The search-results page serves the thumbnail
	// from a handful of rotating subdomains under these roots.
	".gr-assets.com":       {},
	".photo.goodreads.com": {},
}

// hostAllowed reports whether a URL host clears the allow-list. An
// exact match wins first; otherwise any entry with a leading "." is
// treated as a suffix match against the host.
func hostAllowed(host string) bool {
	if _, ok := AllowedCoverHosts[host]; ok {
		return true
	}
	for pattern := range AllowedCoverHosts {
		if len(pattern) > 0 && pattern[0] == '.' && strings.HasSuffix(host, pattern) {
			return true
		}
	}
	return false
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
	if err != nil || u.Scheme != "https" {
		slog.Warn("cover URL rejected: non-https or unparseable",
			"book", bookID, "url", rawURL, "err", err)
		return "", ErrBadCoverURL
	}
	if !hostAllowed(u.Host) {
		// Surface the host in the error so admins can add it to the
		// allow-list after auditing. Logging too so the offending URL
		// is findable in server logs without a repro.
		slog.Warn("cover URL rejected: host not allow-listed",
			"book", bookID, "host", u.Host, "url", rawURL)
		return "", fmt.Errorf("%w: host %q not in allow-list", ErrBadCoverURL, u.Host)
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

	mime := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		return "", errors.New("not an image")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxCoverBytes {
		return "", errors.New("cover too large")
	}

	if err := s.covers.SaveBook(bookID, body); err != nil {
		return "", err
	}
	if err := s.libs.SetCover(ctx, bookID, true, mime); err != nil {
		return "", err
	}
	return mime, nil
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
	go func() {
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.settings.RecordError(ctx, string(id), msg); err != nil {
			slog.Warn("provider health write (error)", "provider", id, "err", err)
		}
	}()
}

