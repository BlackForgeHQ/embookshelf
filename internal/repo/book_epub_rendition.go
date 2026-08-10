// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// BookEpubRenditionRepo owns book_epub_renditions: the tracking row for
// a book's generated EPUB (ADR-0034). One row per book; regeneration
// overwrites. Same lifecycle shape as the markdown rendition, plus the
// file_id pointer at the files row the artifact lives in.
type BookEpubRenditionRepo struct {
	db *db.DB
}

func NewBookEpubRenditionRepo(d *db.DB) *BookEpubRenditionRepo {
	return &BookEpubRenditionRepo{db: d}
}

const epubRenditionCols = `
	book_id, state, error, file_id,
	source_content_hash, converter_version, created_at, updated_at`

// Start upserts the row to pending with a clean error channel. The last
// good artifact fields survive until a new ready overwrites them.
func (r *BookEpubRenditionRepo) Start(ctx context.Context, bookID string) error {
	const q = `
		INSERT INTO book_epub_renditions (book_id, state)
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
func (r *BookEpubRenditionRepo) MarkRunning(ctx context.Context, bookID string) error {
	const q = `
		UPDATE book_epub_renditions SET
			state = 'running', error = '', updated_at = now()
		WHERE book_id = $1
	`
	return execOne(ctx, r.db.SQL, q, bookID)
}

// MarkReady records a finished render: which files row holds the EPUB
// and the provenance that decides staleness later.
func (r *BookEpubRenditionRepo) MarkReady(
	ctx context.Context, bookID, fileID string, sourceHash []byte, version string,
) error {
	const q = `
		UPDATE book_epub_renditions SET
			state               = 'ready',
			error               = '',
			file_id             = $2,
			source_content_hash = $3,
			converter_version   = $4,
			updated_at          = now()
		WHERE book_id = $1
	`
	return execOne(ctx, r.db.SQL, q, bookID, fileID, sourceHash, version)
}

// MarkFailed records why, verbatim — what lands here is exactly what
// the status API surfaces (ADR-0033 §5).
func (r *BookEpubRenditionRepo) MarkFailed(ctx context.Context, bookID, msg string) error {
	const q = `
		UPDATE book_epub_renditions SET
			state = 'failed', error = $2, updated_at = now()
		WHERE book_id = $1
	`
	return execOne(ctx, r.db.SQL, q, bookID, msg)
}

// GetByBookID loads the row, ErrNotFound when the book has none.
func (r *BookEpubRenditionRepo) GetByBookID(ctx context.Context, bookID string) (model.EpubRendition, error) {
	const q = `SELECT ` + epubRenditionCols + ` FROM book_epub_renditions WHERE book_id = $1`

	var (
		m     model.EpubRendition
		state string
	)
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := row.Scan(
		&m.BookID, &state, &m.Error, &m.FileID,
		&m.SourceContentHash, &m.ConverterVersion, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if dberr.IsNotFound(err) {
			return model.EpubRendition{}, ErrNotFound
		}
		return model.EpubRendition{}, err
	}
	m.State = model.MarkdownRenditionState(state)
	return m, nil
}
