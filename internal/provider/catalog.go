// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

// RateLimitConfig tunes the per-provider token-bucket rate limiter.
// RPS is the sustained rate; Burst is the maximum tokens available
// for short spikes.
type RateLimitConfig struct {
	RPS   float64
	Burst int
}

// Info is the static description of a provider — its id, display name,
// and whether it fans out to an external API. The enabled flag is
// per-instance runtime state and lives in provider_settings (repo layer),
// not here.
type Info struct {
	ID             Source
	Name           string
	External       bool
	DefaultEnabled bool
	RateLimit      RateLimitConfig
}

// Catalog is the single source of truth for which providers this binary
// knows how to build. Any new source added here must also appear in
// Build() above; both the handler DTO and the settings seed walk this list.
var Catalog = []Info{
	{ID: SourceGoogleBooks, Name: "Google Books", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 1, Burst: 3}},
	{ID: SourceOpenLibrary, Name: "Open Library", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 2, Burst: 5}},
	{ID: SourceHardcover, Name: "Hardcover", External: true,
		RateLimit: RateLimitConfig{RPS: 1, Burst: 1}},
	{ID: SourceGoodReads, Name: "Goodreads", External: true,
		RateLimit: RateLimitConfig{RPS: 0.5, Burst: 1}},
	{ID: SourceAmazon, Name: "Amazon", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 2, Burst: 5}},
	{ID: SourceDuckDuckGo, Name: "DuckDuckGo", External: true, DefaultEnabled: true,
		RateLimit: RateLimitConfig{RPS: 1, Burst: 2}},
}

// CatalogLookup exposes O(1) id → Info lookup for validation.
func CatalogLookup(id string) (Info, bool) {
	for _, c := range Catalog {
		if string(c.ID) == id {
			return c, true
		}
	}
	return Info{}, false
}
