// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"fmt"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// BookReadingGuideRepo persists LLM-written reading guides, one row per
// book (ADR-0024). Deliberately separate from BookRepo: a guide is
// derived text, and a column on books would carry it through
// UpdateMetadata into the sidecar and the reader's own file.
type BookReadingGuideRepo struct {
	db *db.DB
}

func NewBookReadingGuideRepo(d *db.DB) *BookReadingGuideRepo {
	return &BookReadingGuideRepo{db: d}
}

const readingGuideCols = `
	book_id, about, audience, not_for, problems,
	source_kind, model, language, generated_at, edited_by_user
`

// Upsert writes a freshly generated guide, replacing whatever was there.
//
// edited_by_user resets to false: the text is machine-written again, and
// leaving the flag set would exclude the book from every future bulk run.
// Callers that must not clobber a hand edit check before calling — the
// bulk run does exactly that via ListBookIDsNeedingGuide.
func (r *BookReadingGuideRepo) Upsert(ctx context.Context, g model.ReadingGuide) error {
	const q = `
		INSERT INTO book_reading_guides (
			book_id, about, audience, not_for, problems,
			source_kind, model, language, generated_at, edited_by_user
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), false)
		ON CONFLICT (book_id) DO UPDATE SET
			about          = EXCLUDED.about,
			audience       = EXCLUDED.audience,
			not_for        = EXCLUDED.not_for,
			problems       = EXCLUDED.problems,
			source_kind    = EXCLUDED.source_kind,
			model          = EXCLUDED.model,
			language       = EXCLUDED.language,
			generated_at   = now(),
			edited_by_user = false
	`
	_, err := r.db.SQL.ExecContext(ctx, q,
		g.BookID, g.About, g.Audience, g.NotFor, g.Problems,
		string(g.SourceKind), g.Model, g.Language,
	)
	return err
}

// SaveEdit replaces the prose with a human's and marks the row edited, so
// a bulk run leaves it alone (ADR-0024 §5). Provenance is untouched: the
// generation the edit was made on top of is still what it came from.
//
// Returns ErrNotFound when there is no guide to edit.
func (r *BookReadingGuideRepo) SaveEdit(ctx context.Context, bookID string, t model.ReadingGuideText) error {
	const q = `
		UPDATE book_reading_guides
		SET about = $2, audience = $3, not_for = $4, problems = $5,
		    edited_by_user = true
		WHERE book_id = $1
	`
	res, err := r.db.SQL.ExecContext(ctx, q, bookID, t.About, t.Audience, t.NotFor, t.Problems)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *BookReadingGuideRepo) GetByBookID(ctx context.Context, bookID string) (model.ReadingGuide, error) {
	const q = `SELECT ` + readingGuideCols + ` FROM book_reading_guides WHERE book_id = $1`

	var (
		g           model.ReadingGuide
		sourceKind  string
		generatedAt any
	)
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := row.Scan(
		&g.BookID, &g.About, &g.Audience, &g.NotFor, &g.Problems,
		&sourceKind, &g.Model, &g.Language, &generatedAt, &g.EditedByUser,
	); err != nil {
		if dberr.IsNotFound(err) {
			return model.ReadingGuide{}, ErrNotFound
		}
		return model.ReadingGuide{}, err
	}
	g.SourceKind = model.GuideSource(sourceKind)
	if err := db.ScanTime(generatedAt, &g.GeneratedAt); err != nil {
		return model.ReadingGuide{}, fmt.Errorf("scan generated_at: %w", err)
	}
	return g, nil
}

// ListBookIDsNeedingGuide returns the books a bulk Guide run should
// process: those with no guide, plus those whose guide is machine-written
// and can therefore be replaced. Hand-edited guides are excluded — the
// run must not erase work someone did by hand.
func (r *BookReadingGuideRepo) ListBookIDsNeedingGuide(ctx context.Context) ([]string, error) {
	const q = `
		SELECT b.id
		FROM books b
		LEFT JOIN book_reading_guides g ON g.book_id = b.id
		WHERE b.deleted_at IS NULL
		  AND (g.book_id IS NULL OR g.edited_by_user = false)
		ORDER BY b.created_at
	`
	rows, err := r.db.SQL.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
