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
type EnrichmentService struct {
	providers []provider.Provider
	libs      *repo.LibraryRepo
	covers    *coverstore.Store
	http      *http.Client
}

func NewEnrichmentService(providers []provider.Provider, libs *repo.LibraryRepo, covers *coverstore.Store) *EnrichmentService {
	return &EnrichmentService{
		providers: providers,
		libs:      libs,
		covers:    covers,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// Search queries every provider in parallel and returns a merged, sorted
// slice. A provider failure is logged but does not abort the fan-out.
func (s *EnrichmentService) Search(ctx context.Context, q provider.Query) ([]provider.Match, error) {
	if len(s.providers) == 0 {
		return nil, nil
	}
	// Short-circuit on empty query to avoid hitting the network with noise.
	if strings.TrimSpace(q.Title) == "" && strings.TrimSpace(q.Author) == "" && strings.TrimSpace(q.ISBN) == "" {
		return nil, nil
	}

	g, gctx := errgroup.WithContext(ctx)
	var (
		mu  sync.Mutex
		all []provider.Match
	)
	for _, p := range s.providers {
		p := p
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

// AllowedCoverHosts caps which remote hosts we'll fetch cover bytes from.
// Providers hand back URLs; we refuse anything else to keep this endpoint
// from becoming an open proxy.
var AllowedCoverHosts = map[string]struct{}{
	"books.google.com":           {},
	"books.googleusercontent.com": {},
	"covers.openlibrary.org":     {},
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
