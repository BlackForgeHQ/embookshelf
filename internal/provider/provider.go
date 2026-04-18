// Package provider contains external metadata sources. Each implementation
// takes a Query (title/author/isbn) and returns ranked Matches. The
// EnrichmentService fans queries out across providers concurrently and the
// UI surfaces the merged, sorted results.
package provider

import (
	"context"
	"net/http"
	"time"
)

// Source tags identify the origin of a match in the UI ("google_books", etc.).
type Source string

const (
	SourceGoogleBooks Source = "google_books"
	SourceOpenLibrary Source = "open_library"
)

// Query is the search input. Empty fields are ignored — providers compose
// whatever they need from what's populated.
type Query struct {
	Title  string
	Author string
	ISBN   string
}

// Match is a normalized book hit from any provider. Providers fill what they
// know; consumers merge and prefer higher-confidence rows.
type Match struct {
	Source      Source
	SourceID    string
	Title       string
	Authors     []string
	Description string
	Publisher   string
	Year        int
	ISBN        string
	Series      string
	Categories  []string
	Language    string
	CoverURL    string
	// Confidence is a heuristic 0-100 score — higher is a better guess at
	// being the right book. Providers score their own hits; the service
	// sorts merged results by this.
	Confidence int
}

// Provider is the strategy interface implemented per external source.
type Provider interface {
	Name() Source
	Search(ctx context.Context, q Query) ([]Match, error)
}

// defaultHTTPClient is shared across providers so connection pools get reused.
// A 10 s timeout keeps one slow provider from stalling the whole fan-out.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}
