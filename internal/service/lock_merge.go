package service

import "github.com/blackforge/embookshelf/internal/model"

// MergeLocked applies a lock-aware merge of file-extracted book
// metadata onto the current DB row. For each editable field, if
// the corresponding *_locked flag on current is true, current's
// value wins; otherwise extracted's value wins.
//
// Structural fields (ID, LibraryID, Path, Format, CreatedAt) are
// always preserved from current — extracted is the "shape that came
// out of the file/sidecar," not a complete book row.
//
// This is a pure function so callers (currently library scan's
// re-extract path; possibly future enrichment paths) can compose it
// consistently. Lock contract documented in
// docs/spec/sidecar-write.spec.md §7 and ADR 0001.
func MergeLocked(current, extracted model.Book) model.Book {
	out := current

	if !current.Locks.Title {
		out.Title = extracted.Title
	}
	if !current.Locks.Subtitle {
		out.Subtitle = extracted.Subtitle
	}
	if !current.Locks.Author {
		out.Author = extracted.Author
	}
	if !current.Locks.Description {
		out.Description = extracted.Description
	}
	if !current.Locks.Publisher {
		out.Publisher = extracted.Publisher
	}
	if !current.Locks.Series {
		out.Series = extracted.Series
		out.SeriesIndex = extracted.SeriesIndex
		out.SeriesTotal = extracted.SeriesTotal
	}
	if !current.Locks.ISBN {
		out.ISBN = extracted.ISBN
	}
	if !current.Locks.ISBN10 {
		out.ISBN10 = extracted.ISBN10
	}
	if !current.Locks.Language {
		out.Language = extracted.Language
	}
	if !current.Locks.PublishDate {
		out.PublishDate = extracted.PublishDate
	}
	if !current.Locks.Genres {
		out.Genres = extracted.Genres
	}
	if !current.Locks.Moods {
		out.Moods = extracted.Moods
	}
	if !current.Locks.Tags {
		out.Tags = extracted.Tags
	}
	if !current.Locks.Pages {
		out.Pages = extracted.Pages
	}
	// Cover lock blocks cover replacement; cover bytes/hash are
	// handled separately by the scan path (not in this scalar
	// merge).
	_ = current.Locks.Cover
	return out
}
