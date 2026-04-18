package model

import "time"

type Library struct {
	ID        string
	Name      string
	Slug      string
	BookCount int
	CreatedAt time.Time
}

// LibraryPath is a filesystem root that a library scans for books.
type LibraryPath struct {
	ID              string
	LibraryID       string
	Path            string
	LastScannedAt   *time.Time
	FileCount       int
	DiscoveredCount int
	CreatedAt       time.Time
}

type Book struct {
	ID           string
	LibraryID    string
	Title        string
	Author       string
	Format       string
	Year         int
	// Progress is the *current user's* reading progress (0-100), populated by
	// the LEFT JOIN on user_book_progress. Zero when there's no row yet.
	Progress     int
	Rating       int
	CoverPalette string
	Description  string
	ISBN         string
	Publisher    string
	Series       string
	SeriesIndex  int
	Tags         []string
	CreatedAt    time.Time
	// Path is the absolute file path on disk (or empty for seed data).
	Path string
	// HasCover is true when a cover image lives under coverstore/books/{ID}.
	HasCover bool
	// CoverMime is the image's content type (e.g. "image/jpeg"). Set only
	// when HasCover is true.
	CoverMime string
	// ResumeCFI is the current user's last-known reading position (EPUB CFI).
	// Empty when the user hasn't opened the reader yet.
	ResumeCFI string
}

// Annotation is a single highlight or margin note attached to a book by a
// specific user. The kind (highlight vs. note) is derived from which
// string fields are populated — see the migration comment.
type Annotation struct {
	ID           string
	UserID       string
	BookID       string
	Locator      string
	SelectedText string
	Note         string
	Color        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Shelf struct {
	ID        string
	UserID    string
	Name      string
	Slug      string
	Accent    string
	BookCount int
	IsSmart   bool
	// Rule is non-nil iff IsSmart is true. Marshaled to/from a JSONB
	// column; compiled to SQL at the repo layer.
	Rule      *ShelfRule
	CreatedAt time.Time
}

// ShelfAccents is the palette picker used by shelf creation.
var ShelfAccents = []string{"accent", "teal", "olive", "rust", "plum", "ochre", "forest", "brick"}

// SearchParams controls the Library search/filter/sort surface.
type SearchParams struct {
	Query  string   // free-text search over title/author/series/description
	Format []string // filter by format (empty = all)
	Sort   string   // one of: title, author, recent, year, rating
}

// Formats exposes the formats supported in the UI filter chips.
var Formats = []string{"EPUB", "PDF", "CBZ", "MP3"}

// DedupTags returns tags with duplicates (case-sensitive) removed, preserving
// the order of first occurrence.
func DedupTags(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// SortOptions lists the sort choices exposed in the library toolbar.
var SortOptions = []struct {
	Key, Label string
}{
	{"title", "Title"},
	{"author", "Author"},
	{"recent", "Recent"},
	{"year", "Year"},
	{"rating", "Rating"},
}
