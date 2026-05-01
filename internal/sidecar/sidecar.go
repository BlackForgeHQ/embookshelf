// Package sidecar reads and writes per-book metadata files that live
// next to the book bytes on disk (or in object storage). Two formats:
// metadata.opf (Calibre-compatible XML, read-only) and
// <basename>.embookshelf.json (native, read+write, paired filename).
package sidecar

// Sidecar holds the editable subset of a book's metadata. Fields
// match what the edit-metadata UI exposes; anything ground-truth-
// derived (page count, duration, cover bytes) stays in the embedded
// extractor's output and is not overwritten by sidecars.
type Sidecar struct {
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

// IsZero reports whether s carries no information. Used to short-
// circuit the merge when no sidecar was present.
func (s Sidecar) IsZero() bool {
	return s.Title == "" && s.Subtitle == "" &&
		s.Author == "" && s.Description == "" && s.Language == "" &&
		s.Publisher == "" && s.PublishedDate == "" && s.ISBN == "" &&
		s.Series == "" && s.SeriesIndex == 0 &&
		len(s.Tags) == 0 && len(s.Genres) == 0
}

// Merge overlays b on a: any non-zero field in b wins.
func Merge(a, b Sidecar) Sidecar {
	out := a
	if b.Title != "" {
		out.Title = b.Title
	}
	if b.Subtitle != "" {
		out.Subtitle = b.Subtitle
	}
	if b.Author != "" {
		out.Author = b.Author
	}
	if b.Description != "" {
		out.Description = b.Description
	}
	if b.Language != "" {
		out.Language = b.Language
	}
	if b.Publisher != "" {
		out.Publisher = b.Publisher
	}
	if b.PublishedDate != "" {
		out.PublishedDate = b.PublishedDate
	}
	if b.ISBN != "" {
		out.ISBN = b.ISBN
	}
	if b.Series != "" {
		out.Series = b.Series
	}
	if b.SeriesIndex != 0 {
		out.SeriesIndex = b.SeriesIndex
	}
	if len(b.Tags) > 0 {
		out.Tags = b.Tags
	}
	if len(b.Genres) > 0 {
		out.Genres = b.Genres
	}
	return out
}
