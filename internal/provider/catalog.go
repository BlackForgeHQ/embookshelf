package provider

// Info is the static description of a provider — its id, display name,
// and whether it fans out to an external API. The enabled flag is
// per-instance runtime state and lives in provider_settings (repo layer),
// not here.
type Info struct {
	ID       Source
	Name     string
	External bool
}

// Catalog is the single source of truth for which providers this binary
// knows how to build. Any new source added here must also appear in
// Build() above; both the handler DTO and the settings seed walk this list.
var Catalog = []Info{
	{ID: SourceGoogleBooks, Name: "Google Books", External: true},
	{ID: SourceOpenLibrary, Name: "Open Library", External: true},
	{ID: SourceAmazon, Name: "Amazon", External: true},
	{ID: SourceDuckDuckGo, Name: "DuckDuckGo", External: true},
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
