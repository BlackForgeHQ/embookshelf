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
// overwrites. The lifecycle is renditionLifecycle, shared with the
// markdown repo; what is the EPUB's here is the artifact projection —
// the file_id pointer at the files row the artifact lives in.
type BookEpubRenditionRepo struct {
	renditionLifecycle
}

func NewBookEpubRenditionRepo(d *db.DB) *BookEpubRenditionRepo {
	return &BookEpubRenditionRepo{renditionLifecycle{db: d, table: "book_epub_renditions"}}
}

const epubRenditionCols = `
	book_id, state, error, file_id,
	source_content_hash, converter_version, created_at, updated_at`

// MarkReady records a finished render: which files row holds the EPUB
// and the provenance that decides staleness later.
func (r *BookEpubRenditionRepo) MarkReady(
	ctx context.Context, bookID, fileID string, sourceHash []byte, version string,
) error {
	return r.markReady(ctx, bookID, sourceHash, version, `file_id = $5`, fileID)
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
	m.State = model.RenditionState(state)
	return m, nil
}
