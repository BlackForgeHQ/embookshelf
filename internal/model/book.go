// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

type Library struct {
	ID   string
	Name string
	Slug string
	// Path is the filesystem root this library owns. Set at creation
	// time and immutable afterwards — book approvals physically move
	// files under here using the original filename.
	Path            string
	LastScannedAt   *time.Time
	FileCount       int
	DiscoveredCount int
	BookCount       int
	CreatedAt       time.Time
	// BackendID is the FK to storage_backends. nil when the library has
	// not yet been wired to a backend (legacy libraries created before
	// the storage_v2 migration).
	BackendID *string
	// Root is the root path within the backend (e.g. "/books" for a
	// local backend). nil when BackendID is nil.
	Root *string
}

type Book struct {
	ID        string
	LibraryID string
	Title     string
	Subtitle  string
	Author    string
	Format    string
	Year      int
	// PublishDate is the full publication date when known. Year is kept
	// alongside it as a cheap sort/display field — it's populated from
	// the date when PublishDate is set, otherwise preserved as-is.
	PublishDate *time.Time
	// Language is an IETF BCP-47 tag or bare 2-letter code ("en", "de").
	Language string
	// Progress is the *current user's* reading progress (0-100), populated by
	// the LEFT JOIN on user_book_progress. Zero when there's no row yet.
	Progress     int
	Rating       int
	CoverPalette string
	Description  string
	// ISBN is the legacy single-ISBN column; the edit UI now treats it as
	// the ISBN-13 slot. ISBN10 is a separate, optional column.
	ISBN          string
	ISBN10        string
	Publisher     string
	Series        string
	SeriesIndex   int
	SeriesTotal   int
	Genres        []string
	Moods         []string
	Tags          []string
	AgeRating     string
	ContentRating string
	Pages         int
	// PublicReviews is tri-state: nil = unset, non-nil = explicit
	// true/false (spec mirror of the UI's "No Value" option).
	PublicReviews *bool
	CreatedAt     time.Time
	// Path is the absolute file path on disk (or empty for seed data).
	Path string
	// UUID is the stable identifier assigned during the storage_v2
	// backfill. nil until the backfill worker populates it.
	UUID *string
	// FolderPath is the relative folder within the library that contains
	// this book's files. nil until backfill or scan assigns it.
	FolderPath *string
	// HasCover is true when a cover image lives under coverstore/books/{ID}.
	HasCover bool
	// CoverMime is the image's content type (e.g. "image/jpeg"). Set only
	// when HasCover is true.
	CoverMime string
	// CoverHash is the sha256 of the cover image bytes, set after the cover
	// is promoted from bookdrop or after the boot-time backfill runs.
	// nil means "not yet hashed" — the serve handler falls back to the
	// legacy book-id-keyed path until the backfill fills it in.
	CoverHash []byte
	// ResumeCFI is the current user's last-known reading position (EPUB CFI).
	// Empty when the user hasn't opened the reader yet.
	ResumeCFI string
	// ResumeAudio is the same reader's position in the book's narration.
	// A second column because the two are different currencies until the
	// alignment map bridges them, and one column meant each Rendition
	// overwrote the other's place (#200).
	ResumeAudio string
	// Audiobook fields — populated for MP3 / M4B and left zero for other
	// formats. DurationSeconds is nil when unknown (no MP3 frame header,
	// no MP4 mvhd atom, or simply non-audio); the UI treats nil as "—".
	DurationSeconds *int
	Narrator        string
	// Chapters is the audiobook chapter list (or EPUB TOC, future use).
	// Stored as a JSON document on disk; nil means "no chapter data".
	Chapters []Chapter
	// Locks pins individual metadata fields against automatic overwrite.
	// The apply-metadata flow consults each flag before copying a
	// candidate value over the stored one; manual PATCHes always write.
	Locks BookLocks
}

// Chapter is one entry in the audiobook chapter list. Times are in
// seconds (float so we can preserve sub-second M4B metadata without
// quantization). Title is non-empty; an "Untitled chapter N" fallback
// is the processor's job, not the model's.
type Chapter struct {
	Title  string  `json:"title"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
}

// BookLocks and the lock vocabulary live in locks.go.

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
	Icon      string
	BookCount int
	IsSmart   bool
	// Rule is non-nil iff IsSmart is true. Marshaled to/from a JSONB
	// column; compiled to SQL at the repo layer.
	Rule      *ShelfRule
	CreatedAt time.Time
	// IsPublic flips a regular shelf into the cross-user "Shared
	// shelves" surface. CHECK constraint forbids public+smart.
	IsPublic bool
	// OwnerName is filled by the repo when the viewer is not the
	// owner — used by the sidebar tooltip on public shelves. Empty for
	// shelves the viewer owns.
	OwnerName string
}

// ShelfAccents is the palette picker used by shelf creation.
var ShelfAccents = []string{"accent", "teal", "olive", "rust", "plum", "ochre", "forest", "brick"}

// SearchParams controls the Library search/filter/sort surface.
type SearchParams struct {
	Query     string   // free-text search over title/author/series/description
	Format    []string // filter by format (empty = all)
	Sort      string   // one of: title, author, recent, year, rating
	Unshelved bool     // restrict to books not on any of the user's regular non-system shelves
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

// EditableMetadata is the canonical shape for the editable subset of
// a book's metadata — the fields a user can change in the edit-metadata
// UI, the fields a sidecar carries, the fields an in-file Embedder
// writes back into the book file. Read-only audio fields (Duration,
// Narrator) and structural fields (ID, LibraryID, Path) live on Book,
// not here.
//
// JSON tags match docs/spec/sidecar-write.spec.md §4.1's wire format.
type EditableMetadata struct {
	Title         string   `json:"title,omitempty"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Author        string   `json:"author,omitempty"`
	Description   string   `json:"description,omitempty"`
	Language      string   `json:"language,omitempty"`
	Publisher     string   `json:"publisher,omitempty"`
	PublishedDate string   `json:"published_date,omitempty"`
	ISBN          string   `json:"isbn,omitempty"`
	Series        string   `json:"series,omitempty"`
	SeriesIndex   int      `json:"series_index,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Genres        []string `json:"genres,omitempty"`
}

// IsZero reports whether em carries no information. Used to short-
// circuit the merge when no sidecar/payload was present. Derived from
// editableFields.
func (em EditableMetadata) IsZero() bool {
	for _, f := range editableFields {
		if !f.Empty(em) {
			return false
		}
	}
	return true
}

// MergeEditable overlays b on a: any non-zero field in b wins. Derived
// from editableFields.
func MergeEditable(a, b EditableMetadata) EditableMetadata {
	out := a
	for _, f := range editableFields {
		if !f.Empty(b) {
			f.Copy(&out, b)
		}
	}
	return out
}

// Editable returns the editable scalar subset of b. Drops IDs,
// structural fields, audio fields, locks. PublishedDate is left blank
// — Book.PublishDate is *time.Time; conversion lives at the boundary,
// which is why that field declares no Book projection.
func (b Book) Editable() EditableMetadata {
	var em EditableMetadata
	for _, f := range editableFields {
		if f.FromBook != nil {
			f.FromBook(&em, b)
		}
	}
	return em
}

// ApplyEditable copies em's fields onto b. Does not touch IDs,
// structural fields, audio fields, locks, or PublishDate (caller
// converts string <-> *time.Time at the boundary).
func (b *Book) ApplyEditable(em EditableMetadata) {
	for _, f := range editableFields {
		if f.ToBook != nil {
			f.ToBook(b, em)
		}
	}
}
