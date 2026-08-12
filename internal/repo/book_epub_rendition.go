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

// epubRenditionProjection is the book_epub_renditions row, declared
// once. The lifecycle columns (state, error, provenance) are shared
// with the markdown rendition's projection; file_id is the EPUB's own
// artifact column, the pointer at the files row the generated EPUB
// lives in.
var epubRenditionProjection = projection[model.EpubRendition]{
	{name: "book_id", dest: func(m *model.EpubRendition) any { return &m.BookID }},
	{name: "state", dest: func(m *model.EpubRendition) any { return &m.State }},
	{name: "error", dest: func(m *model.EpubRendition) any { return &m.Error }},
	{name: "file_id", dest: func(m *model.EpubRendition) any { return &m.FileID }},
	{name: "source_content_hash", dest: func(m *model.EpubRendition) any { return &m.SourceContentHash }},
	{name: "converter_version", dest: func(m *model.EpubRendition) any { return &m.ConverterVersion }},
	{name: "created_at", dest: func(m *model.EpubRendition) any { return &m.CreatedAt }},
	{name: "updated_at", dest: func(m *model.EpubRendition) any { return &m.UpdatedAt }},
}

// epubRenditionCols is the projection rendered for the unaliased
// book_epub_renditions queries.
var epubRenditionCols = epubRenditionProjection.returningList("book_epub_renditions")

// MarkReady records a finished render: which files row holds the EPUB
// and the provenance that decides staleness later.
func (r *BookEpubRenditionRepo) MarkReady(
	ctx context.Context, bookID, fileID string, sourceHash []byte, version string,
) error {
	return r.markReady(ctx, bookID, sourceHash, version, `file_id = $5`, fileID)
}

// GetByBookID loads the row, ErrNotFound when the book has none.
func (r *BookEpubRenditionRepo) GetByBookID(ctx context.Context, bookID string) (model.EpubRendition, error) {
	q := `SELECT ` + epubRenditionCols + ` FROM book_epub_renditions WHERE book_id = $1`

	var m model.EpubRendition
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := epubRenditionProjection.scan(row, &m); err != nil {
		if dberr.IsNotFound(err) {
			return model.EpubRendition{}, ErrNotFound
		}
		return model.EpubRendition{}, err
	}
	return m, nil
}
