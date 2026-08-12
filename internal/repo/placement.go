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

// placementProjection is the ListPlacements row, declared once. It had
// zero direct tests before this — only the recovery integration test
// exercised it — despite spanning two tables' worth of same-type columns
// in one SELECT: title/author/format/book_path are four adjacent TEXT
// columns from books, and file_id/location sit next to them from the
// files side of the join. A crossed pair here would compile, run, and
// point the misplaced-file recovery flow at the wrong book or the wrong
// key.
//
// Every entry hardcodes its own table's alias (b. or f.) in expr rather
// than using the {alias} substitution token: the projection's usual
// single-alias form doesn't fit a two-table join, and expr already
// exists for exactly this case (a computed/joined column, per the
// projection's documented contract). size uses COALESCE for the same
// reason the book/shelf projections' joined columns do — a book with no
// files row LEFT JOINs to NULL, and folding that into the SQL keeps the
// scan destination a plain int64 instead of a nullable adapter.
var placementProjection = projection[Placement]{
	{name: "book_id", expr: "b.id", dest: func(p *Placement) any { return &p.BookID }},
	{name: "title", expr: "b.title", dest: func(p *Placement) any { return &p.Title }},
	{name: "author", expr: "b.author", dest: func(p *Placement) any { return &p.Author }},
	{name: "format", expr: "b.format", dest: func(p *Placement) any { return &p.Format }},
	{name: "book_path", expr: "b.path", dest: func(p *Placement) any { return &p.BookPath }},
	{name: "file_id", expr: "f.id", dest: func(p *Placement) any { return nullText{Dst: &p.FileID} }},
	{name: "location", expr: "f.location", dest: func(p *Placement) any { return nullText{Dst: &p.Location} }},
	{name: "size", expr: "COALESCE(f.size, 0)", dest: func(p *Placement) any { return &p.Size }},
	{name: "content_hash", expr: "f.content_hash", dest: func(p *Placement) any { return &p.ContentHash }},
}

// placementCols is the projection rendered once. The alias argument is
// unused — every column's expr names its own table explicitly, so there
// is no {alias} token for render to substitute.
var placementCols = placementProjection.selectList("")

// ListPlacements returns every (book, files row) pair in a library,
// including books whose files rows are gone. Ordered by book id then
// location so a report over it is stable between runs.
func (r *BookRepo) ListPlacements(ctx context.Context, libraryID string) ([]Placement, error) {
	q := `
		SELECT ` + placementCols + `
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
		err := placementProjection.scan(s, &p)
		return p, err
	})
}
