// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
)

// Placement is one answer to "where does the catalog say this book's
// bytes are". A book with two files rows (an EPUB and its narration)
// yields two Placements; a book whose files row the missing-purge has
// already deleted yields one with FileID empty, carrying only what
// books.path says.
//
// That last shape is the whole reason this is a LEFT JOIN and not a
// files query: the 24h purge (task.RunMissingPurge) deletes the row of a
// file the scan could not find, and a scan will never put it back
// (ADR-0018 — a scan is drift detection, never an ingest path). The book
// still names its key in books.path, and that is the only remaining
// record that the bytes were ever placed.
type Placement struct {
	BookID   string
	Title    string
	Author   string
	Format   string
	BookPath string

	// FileID is empty when no files row survives for this book.
	FileID      string
	Location    string
	Size        int64
	ContentHash []byte
}

// HasFileRow reports whether a files row backs this placement.
func (p Placement) HasFileRow() bool { return p.FileID != "" }

// ListPlacements returns every (book, files row) pair in a library,
// including books whose files rows are gone. Ordered by book id then
// location so a report over it is stable between runs.
func (r *BookRepo) ListPlacements(ctx context.Context, libraryID string) ([]Placement, error) {
	const q = `
		SELECT b.id, b.title, b.author, b.format, b.path,
		       f.id, f.location, f.size, f.content_hash
		FROM books b
		LEFT JOIN files f ON f.book_id = b.id
		WHERE b.library_id = $1 AND b.deleted_at IS NULL
		ORDER BY b.id, f.location
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, libraryID)
	if err != nil {
		return nil, err
	}
	return collect(rows, []Placement{}, func(s scanner) (Placement, error) {
		var p Placement
		var size *int64
		err := s.Scan(
			&p.BookID, &p.Title, &p.Author, &p.Format, &p.BookPath,
			nullText{Dst: &p.FileID}, nullText{Dst: &p.Location}, &size, &p.ContentHash,
		)
		if size != nil {
			p.Size = *size
		}
		return p, err
	})
}
