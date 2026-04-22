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
}

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
		providers: providers,
		settings:  settings,
		libs:      libs,
		covers:    covers,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// Search queries every enabled provider in parallel and returns a merged,
// sorted slice. A provider failure is logged but does not abort the
// fan-out. Disabled providers are skipped without hitting the network.
func (s *EnrichmentService) Search(ctx context.Context, q provider.Query) ([]provider.Match, error) {
	if len(s.providers) == 0 {
		return nil, nil
	}
	// Short-circuit on empty query to avoid hitting the network with noise.
	if strings.TrimSpace(q.Title) == "" && strings.TrimSpace(q.Author) == "" && strings.TrimSpace(q.ISBN) == "" {
		return nil, nil
	}

	// Fetch the live enabled map; on DB error fall through to the full
	// provider list so an outage of the settings table doesn't silently
	// disable enrichment entirely. Best-effort graceful degrade.
	enabled, err := s.settings.EnabledIDs(ctx)
	if err != nil {
		slog.Warn("provider settings fetch — running all providers", "err", err)
		enabled = nil
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
	return all, nil
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
