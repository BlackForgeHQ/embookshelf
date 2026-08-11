// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// EpubRendition mirrors one book_epub_renditions row: the tracking
// record for the EPUB rendered from a PDF book's Markdown rendition
// (ADR-0034). The artifact itself is a files row on the same book;
// FileID is the pointer that says which one.
type EpubRendition struct {
	BookID string
	// State is the shared rendition lifecycle — one HTTP call, no
	// cancel, no segments.
	State RenditionState
	// Error is the loud-failure channel, surfaced verbatim — including
	// a chained markdown-conversion failure.
	Error string
	// FileID names the generated files row. Nil before first ready, and
	// again if the file goes missing and is purged (ON DELETE SET NULL).
	FileID *string
	// SourceContentHash is the PDF the chain started from. Mismatch
	// with the book's current primary file means stale — labelled,
	// never auto-invalidated.
	SourceContentHash []byte
	ConverterVersion  string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
