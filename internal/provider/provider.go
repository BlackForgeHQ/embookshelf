// Package provider contains external metadata sources. Each implementation
// takes a Query (title/author/isbn) and returns ranked Matches. The
// EnrichmentService fans queries out across providers concurrently and the
// UI surfaces the merged, sorted results.
package provider

import (
	"context"
)

// Source tags identify the origin of a match in the UI ("google_books", etc.).
type Source string

const (
	SourceGoogleBooks Source = "google_books"
	SourceOpenLibrary Source = "open_library"
	SourceAmazon      Source = "amazon"
	SourceDuckDuckGo  Source = "duckduckgo"
	SourceHardcover   Source = "hardcover"
	SourceGoodReads   Source = "goodreads"
)

// Build constructs a provider by name. Returns nil for unknown sources
// — callers (the bootstrap config in main.go) log + skip those rather
// than failing startup, so a typo doesn't crash the whole server.
func Build(name Source) Provider {
	info, ok := CatalogLookup(string(name))
	if !ok {
		return nil
	}
	client := NewResilientClient(
		string(name),
		info.RateLimit.RPS,
		info.RateLimit.Burst,
	)
	switch name {
	case SourceGoogleBooks:
		return NewGoogleBooks(client)
	case SourceOpenLibrary:
		return NewOpenLibrary(client)
	case SourceAmazon:
		return NewAmazon(client)
	case SourceDuckDuckGo:
		return NewDuckDuckGo(client)
	case SourceHardcover:
		return NewHardcover(client)
	case SourceGoodReads:
		return NewGoodReads(client)
	}
	return nil
}

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

// Configurable is implemented by providers that accept runtime config
// (API keys, region, language, cookie, …). The enrichment service calls
// Configure on boot with the stored JSON blob, and again whenever an
// admin writes new config via the settings UI.
//
// Providers that don't need any config simply don't implement this
// interface; the service type-asserts and skips them.
type Configurable interface {
	Configure(raw []byte) error
}

// ConfigFieldKind describes how the admin UI should render a config
// input. Providers expose their schema via ConfigSchema so the panel
// doesn't have to know each provider's shape.
type ConfigFieldKind string

const (
	ConfigFieldText     ConfigFieldKind = "text"
	ConfigFieldPassword ConfigFieldKind = "password" // displayed with a "Reveal" toggle; sent as plaintext
	ConfigFieldSelect   ConfigFieldKind = "select"
	ConfigFieldTextarea ConfigFieldKind = "textarea"
)

// ConfigField is one knob on the Settings → Providers panel.
type ConfigField struct {
	// Key matches the JSON key in the stored config blob.
	Key string
	// Label is the form label shown to admins.
	Label string
	// Kind drives the input rendering on the wire.
	Kind ConfigFieldKind
	// Placeholder is an optional hint string shown in the empty input.
	Placeholder string
	// Help is an optional inline help line rendered under the field.
	Help string
	// Options populate select-kind fields. Keys are the stored values,
	// labels are the display strings. Ignored for non-select kinds.
	Options []ConfigOption
}

type ConfigOption struct {
	Value string
	Label string
}

// SchemaProvider is implemented by providers that expose a config
// schema to the admin UI. Implementors must also implement
// Configurable — the schema is just the "what inputs to render"
// description; Configure does the actual write.
type SchemaProvider interface {
	ConfigSchema() []ConfigField
}

