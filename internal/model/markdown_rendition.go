// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// MarkdownRendition mirrors one book_markdown_renditions row: the
// tracking record for the markdown produced from a Convertible-format
// book by the converter extension.
type MarkdownRendition struct {
	BookID string
	State  RenditionState
	// Error is the loud-failure channel, surfaced verbatim. Empty while
	// nothing is wrong.
	Error string
	// Location is the storage key the markdown lives at, inside the
	// book's own library. Empty until first ready.
	Location  string
	SizeBytes int64
	// SourceContentHash is the primary file the markdown was converted
	// from. A mismatch with the book's current file means stale —
	// labelled, never auto-invalidated.
	SourceContentHash []byte
	ConverterVersion  string
	// Pages is what the conversion could read of a paged source.
	Pages     PageCoverage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PageCoverage is a paged conversion's record of its source (ADR-0040):
// how many pages the document has and which gave no extractable text.
// The zero value is "not recorded" — a format without pages, or a
// rendition converted before the record existed.
type PageCoverage struct {
	PageCount int
	// MissingPages are 1-based page numbers in the converter's compact
	// form, "1,10-15"; empty when nothing was missing.
	MissingPages string
}
