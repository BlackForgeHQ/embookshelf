package service

import (
	"context"
	"errors"
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

	// Result cache keyed by normalized (title|author|isbn). A fresh hit
	// returns without any upstream calls, which matters for Google Books
	// where the public quota is ~100 req / 100s per IP — admins tabbing
	// between books or re-opening the enrichment panel burn through it
	// fast without this.
	cacheMu  sync.Mutex
	cache    map[string]cacheEntry
	cacheTTL time.Duration

	// Per-provider cooldown — when a provider returns 429 we skip it
	// for this long on subsequent Search calls. Prevents the fan-out
	// from re-triggering the rate-limiter we just tripped.
	cooldownMu  sync.Mutex
	cooldown    map[provider.Source]time.Time
	cooldownDur time.Duration
}

type cacheEntry struct {
	matches []provider.Match
	at      time.Time
}

const (
	enrichCacheTTL      = 5 * time.Minute
	enrichCooldownAfter = 60 * time.Second
)

// ErrUnknownProvider is returned by SetProviderEnabled when the caller
// hands in an id the binary doesn't recognize.
var ErrUnknownProvider = errors.New("unknown provider")

// ProviderInfo is the handler-facing DTO shape: static catalog facts
// joined with the live enabled flag from the DB.
type ProviderInfo struct {
	ID       provider.Source
	Name     string
	Enabled  bool
	External bool
}

func NewEnrichmentService(
	providers []provider.Provider,
	settings *repo.ProviderSettingsRepo,
	libs *repo.LibraryRepo,
	covers *coverstore.Store,
) *EnrichmentService {
	return &EnrichmentService{
		providers:   providers,
		settings:    settings,
		libs:        libs,
		covers:      covers,
		http:        &http.Client{Timeout: 15 * time.Second},
		cache:       make(map[string]cacheEntry),
		cacheTTL:    enrichCacheTTL,
		cooldown:    make(map[provider.Source]time.Time),
		cooldownDur: enrichCooldownAfter,
	}
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

	// Compute the set we'd actually query (pre-cooldown filter) so the
	// UI can say "we searched these three" consistently across cache
	// hits and fresh fan-outs.
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
		if s.providerCoolingDown(p.Name()) {
			// 429 tripped recently — don't re-provoke the rate limiter.
			continue
		}
		g.Go(func() error {
			matches, err := p.Search(gctx, q)
			if err != nil {
				slog.Warn("provider search failed", "provider", p.Name(), "err", err)
				if isRateLimited(err) {
					s.markCooldown(p.Name())
				}
				return nil
			}
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

// ListProviders joins the static catalog with the live enabled map so
// the Settings UI can render one row per known provider. Missing rows
// (catalog entries without a provider_settings row) count as disabled.
func (s *EnrichmentService) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	enabled, err := s.settings.EnabledIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderInfo, 0, len(provider.Catalog))
	for _, c := range provider.Catalog {
		out = append(out, ProviderInfo{
			ID:       c.ID,
			Name:     c.Name,
			External: c.External,
			Enabled:  enabled[string(c.ID)],
		})
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

// AllowedCoverHosts caps which remote hosts we'll fetch cover bytes from.
// Providers hand back URLs; we refuse anything else to keep this endpoint
// from becoming an open proxy.
var AllowedCoverHosts = map[string]struct{}{
	"books.google.com":                {},
	"books.googleusercontent.com":     {},
	"covers.openlibrary.org":          {},
	"images-na.ssl-images-amazon.com": {},
	"m.media-amazon.com":              {},
	"duckduckgo.com":                  {},
}

// ErrBadCoverURL is returned when the URL fails the host/scheme/content checks.
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
		return "", ErrBadCoverURL
	}
	if _, ok := AllowedCoverHosts[u.Host]; !ok {
		return "", ErrBadCoverURL
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
	defer resp.Body.Close()
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

func (s *EnrichmentService) providerCoolingDown(id provider.Source) bool {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	until, ok := s.cooldown[id]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(s.cooldown, id)
		return false
	}
	return true
}

func (s *EnrichmentService) markCooldown(id provider.Source) {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	s.cooldown[id] = time.Now().Add(s.cooldownDur)
}

// isRateLimited inspects an error string for the 429 signal. Provider
// Search functions wrap their HTTP errors loosely (e.g. "google books
// 429") so a substring match is enough — no typed error to unwrap.
func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "429")
}
