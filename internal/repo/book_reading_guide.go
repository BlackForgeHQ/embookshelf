// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"

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

// readingGuideProjection is the book_reading_guides row, declared once.
// about, audience, not_for and problems are four adjacent TEXT columns —
// Upsert restates all ten columns and SaveEdit restates these four —
// so a crossed pair used to compile, run, and answer one of a reader's
// four questions with another's text. Stating a column's position and
// its destination together makes that swap unrepresentable.
var readingGuideProjection = projection[model.ReadingGuide]{
	{name: "book_id", dest: func(g *model.ReadingGuide) any { return &g.BookID }},
	{name: "about", dest: func(g *model.ReadingGuide) any { return &g.About }},
	{name: "audience", dest: func(g *model.ReadingGuide) any { return &g.Audience }},
	{name: "not_for", dest: func(g *model.ReadingGuide) any { return &g.NotFor }},
	{name: "problems", dest: func(g *model.ReadingGuide) any { return &g.Problems }},
	{name: "source_kind", dest: func(g *model.ReadingGuide) any { return &g.SourceKind }},
	{name: "model", dest: func(g *model.ReadingGuide) any { return &g.Model }},
	{name: "language", dest: func(g *model.ReadingGuide) any { return &g.Language }},
	{name: "generated_at", dest: func(g *model.ReadingGuide) any { return &g.GeneratedAt }},
	{name: "edited_by_user", dest: func(g *model.ReadingGuide) any { return &g.EditedByUser }},
}

// readingGuideCols is the projection rendered for the unaliased
// book_reading_guides queries.
var readingGuideCols = readingGuideProjection.returningList("book_reading_guides")

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
	return execOne(ctx, r.db.SQL, q, bookID, t.About, t.Audience, t.NotFor, t.Problems)
}

func (r *BookReadingGuideRepo) GetByBookID(ctx context.Context, bookID string) (model.ReadingGuide, error) {
	q := `SELECT ` + readingGuideCols + ` FROM book_reading_guides WHERE book_id = $1`

	var g model.ReadingGuide
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := readingGuideProjection.scan(row, &g); err != nil {
		if dberr.IsNotFound(err) {
			return model.ReadingGuide{}, ErrNotFound
		}
		return model.ReadingGuide{}, err
	}
	return g, nil
}

// GuideCandidate is a book a Guide run would process. Format comes along
// because the run's pre-flight estimate is computed from it: only EPUB
// sends book text, so only EPUB carries the per-book text cap.
type GuideCandidate struct {
	BookID string
	Format string
}

// ListGuideCandidates returns the books a bulk Guide run should process:
// those with no guide, plus those whose guide is machine-written and can
// therefore be replaced. Hand-edited guides are excluded — the run must
// not erase work someone did by hand.
func (r *BookReadingGuideRepo) ListGuideCandidates(ctx context.Context) ([]GuideCandidate, error) {
	const q = `
		SELECT b.id, b.format
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
	return collect(rows, nil, func(s scanner) (GuideCandidate, error) {
		var c GuideCandidate
		err := s.Scan(&c.BookID, &c.Format)
		return c, err
	})
}

// CountCoverage reports how many books exist and how many already have a
// guide. Both numbers come from one query so they cannot be read a moment
// apart and disagree while a run is landing guides.
//
// Hand-edited guides count as done — a guide is a guide however it was
// written, and excluding them would pin the progress bar below 100% on any
// library where someone edited one.
func (r *BookReadingGuideRepo) CountCoverage(ctx context.Context) (total, done int, err error) {
	const q = `
		SELECT count(*) AS total,
		       count(g.book_id) AS done
		FROM books b
		LEFT JOIN book_reading_guides g ON g.book_id = b.id
		WHERE b.deleted_at IS NULL
	`
	if err := r.db.SQL.QueryRowContext(ctx, q).Scan(&total, &done); err != nil {
		return 0, 0, err
	}
	return total, done, nil
}
