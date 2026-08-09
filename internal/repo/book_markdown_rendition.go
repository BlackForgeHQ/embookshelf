// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// BookMarkdownRenditionRepo owns book_markdown_renditions: the tracking
// row for a book's Markdown rendition (ADR-0033). One row per book;
// regeneration overwrites.
type BookMarkdownRenditionRepo struct {
	db *db.DB
}

func NewBookMarkdownRenditionRepo(d *db.DB) *BookMarkdownRenditionRepo {
	return &BookMarkdownRenditionRepo{db: d}
}

const markdownRenditionCols = `
	book_id, state, error, location, size_bytes,
	source_content_hash, converter_version, created_at, updated_at`

// Start upserts the row to pending with a clean error channel. The last
// good artifact fields (location, hash, version) survive until a new
// ready overwrites them, so a failed regeneration does not orphan the
// bytes a consumer may still be reading.
func (r *BookMarkdownRenditionRepo) Start(ctx context.Context, bookID string) error {
	const q = `
		INSERT INTO book_markdown_renditions (book_id, state)
		VALUES ($1, 'pending')
		ON CONFLICT (book_id) DO UPDATE SET
			state      = 'pending',
			error      = '',
			updated_at = now()
	`
	_, err := r.db.SQL.ExecContext(ctx, q, bookID)
	return err
}

// MarkRunning records that a worker picked the row up.
func (r *BookMarkdownRenditionRepo) MarkRunning(ctx context.Context, bookID string) error {
	return r.transition(ctx, bookID, `state = 'running', error = '', updated_at = now()`)
}

// MarkReady records a finished conversion: where the bytes landed and
// the provenance that decides staleness later.
func (r *BookMarkdownRenditionRepo) MarkReady(
	ctx context.Context, bookID, location string, size int64, sourceHash []byte, version string,
) error {
	const q = `
		UPDATE book_markdown_renditions SET
			state               = 'ready',
			error               = '',
			location            = $2,
			size_bytes          = $3,
			source_content_hash = $4,
			converter_version   = $5,
			updated_at          = now()
		WHERE book_id = $1
	`
	return execOne(ctx, r.db.SQL, q, bookID, location, size, sourceHash, version)
}

// MarkFailed records why, verbatim — what lands here is exactly what the
// status API surfaces (ADR-0033 §5).
func (r *BookMarkdownRenditionRepo) MarkFailed(ctx context.Context, bookID, msg string) error {
	const q = `
		UPDATE book_markdown_renditions SET
			state = 'failed', error = $2, updated_at = now()
		WHERE book_id = $1
	`
	return execOne(ctx, r.db.SQL, q, bookID, msg)
}

func (r *BookMarkdownRenditionRepo) transition(ctx context.Context, bookID, set string) error {
	q := `UPDATE book_markdown_renditions SET ` + set + ` WHERE book_id = $1`
	return execOne(ctx, r.db.SQL, q, bookID)
}

// GetByBookID loads the row, ErrNotFound when the book has none.
func (r *BookMarkdownRenditionRepo) GetByBookID(ctx context.Context, bookID string) (model.MarkdownRendition, error) {
	const q = `SELECT ` + markdownRenditionCols + ` FROM book_markdown_renditions WHERE book_id = $1`

	var (
		m     model.MarkdownRendition
		state string
	)
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := row.Scan(
		&m.BookID, &state, &m.Error, &m.Location, &m.SizeBytes,
		&m.SourceContentHash, &m.ConverterVersion, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if dberr.IsNotFound(err) {
			return model.MarkdownRendition{}, ErrNotFound
		}
		return model.MarkdownRendition{}, err
	}
	m.State = model.MarkdownRenditionState(state)
	return m, nil
}

// ConversionCandidate is a book a bulk conversion run would process.
type ConversionCandidate struct {
	BookID string
}

// ListConversionCandidates returns the Convertible-format books a bulk
// run should convert: those with no rendition row and those whose last
// conversion failed. Ready renditions are left alone even when stale —
// bulk re-converting a library because one file changed is a cost
// decision the per-book button owns, not the bulk switch (mirrors the
// hand-edited-guide exclusion in ListGuideCandidates: one query decides
// what a run may touch).
func (r *BookMarkdownRenditionRepo) ListConversionCandidates(ctx context.Context) ([]ConversionCandidate, error) {
	const q = `
		SELECT b.id
		FROM books b
		LEFT JOIN book_markdown_renditions m ON m.book_id = b.id
		WHERE b.deleted_at IS NULL
		  AND upper(b.format) = ANY($1::text[])
		  AND (m.book_id IS NULL OR m.state = 'failed')
		ORDER BY b.created_at
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, model.ConvertibleFormats())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ConversionCandidate
	for rows.Next() {
		var c ConversionCandidate
		if err := rows.Scan(&c.BookID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConversionCoverage is the settings card's progress answer, over the
// library's Convertible-format books.
type ConversionCoverage struct {
	// Total is every convertible book, deleted excluded.
	Total int
	// Ready renditions, current or stale alike.
	Ready int
	// Converting is pending + running — the moving part a poll watches.
	Converting int
	// Failed renditions, error recorded on the row.
	Failed int
	// Unconverted books have no rendition row at all.
	Unconverted int
}

// CountConversionCoverage answers all five numbers from one query so a
// poll cannot read them a moment apart and disagree mid-run.
func (r *BookMarkdownRenditionRepo) CountConversionCoverage(ctx context.Context) (ConversionCoverage, error) {
	const q = `
		SELECT count(*) AS total,
		       count(*) FILTER (WHERE m.state = 'ready') AS ready,
		       count(*) FILTER (WHERE m.state IN ('pending', 'running')) AS converting,
		       count(*) FILTER (WHERE m.state = 'failed') AS failed,
		       count(*) FILTER (WHERE m.book_id IS NULL) AS unconverted
		FROM books b
		LEFT JOIN book_markdown_renditions m ON m.book_id = b.id
		WHERE b.deleted_at IS NULL
		  AND upper(b.format) = ANY($1::text[])
	`
	var cov ConversionCoverage
	err := r.db.SQL.QueryRowContext(ctx, q, model.ConvertibleFormats()).Scan(
		&cov.Total, &cov.Ready, &cov.Converting, &cov.Failed, &cov.Unconverted,
	)
	return cov, err
}
