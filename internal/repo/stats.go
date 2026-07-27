// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"

	"github.com/blackforge/embookshelf/internal/db"
)

// StatsRepo groups read-only aggregate queries that span multiple
// existing tables. Each method runs an independent SQL statement; the
// service layer fans them out in parallel.
type StatsRepo struct {
	db *db.DB
}

func NewStatsRepo(d *db.DB) *StatsRepo {
	return &StatsRepo{db: d}
}

// CountBooks returns every book not soft-deleted.
func (r *StatsRepo) CountBooks(ctx context.Context) (int, error) {
	var n int
	err := r.db.SQL.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM books WHERE deleted_at IS NULL
	`).Scan(&n)
	return n, err
}

// CountBooksWithCover returns how many non-deleted books carry a cover
// image.  Used as a quick "cover coverage" indicator.
func (r *StatsRepo) CountBooksWithCover(ctx context.Context) (int, error) {
	var n int
	err := r.db.SQL.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM books WHERE deleted_at IS NULL AND has_cover = true
	`).Scan(&n)
	return n, err
}

type StatsBucket struct {
	Label string
	Count int
}

// BooksPerLibrary returns book counts grouped by library, ordered by
// most-populated first. LEFT JOIN keeps empty libraries visible so the
// user can see they exist but haven't been scanned yet.
func (r *StatsRepo) BooksPerLibrary(ctx context.Context) ([]StatsBucket, error) {
	return r.query(ctx, `
		SELECT l.name, COUNT(b.id)
		FROM libraries l
		LEFT JOIN books b ON b.library_id = l.id AND b.deleted_at IS NULL
		GROUP BY l.id, l.name
		ORDER BY COUNT(b.id) DESC, l.name
	`)
}

// BooksPerFormat buckets the library by format string (EPUB / PDF / CBZ / M4B …).
func (r *StatsRepo) BooksPerFormat(ctx context.Context) ([]StatsBucket, error) {
	return r.query(ctx, `
		SELECT COALESCE(NULLIF(format, ''), 'Unknown'), COUNT(*)
		FROM books
		WHERE deleted_at IS NULL
		GROUP BY 1
		ORDER BY 2 DESC
	`)
}

// TopAuthors returns the N most-prolific authors in the collection.
func (r *StatsRepo) TopAuthors(ctx context.Context, limit int) ([]StatsBucket, error) {
	if limit <= 0 {
		limit = 10
	}
	const q = `
		SELECT COALESCE(NULLIF(author, ''), 'Unknown'), COUNT(*)
		FROM books
		WHERE deleted_at IS NULL
		GROUP BY 1
		ORDER BY 2 DESC, 1
		LIMIT $1
	`
	return r.query(ctx, q, limit)
}

// TopTags walks the tags[] columns via UNNEST and returns the N
// most-used tag labels.
func (r *StatsRepo) TopTags(ctx context.Context, limit int) ([]StatsBucket, error) {
	if limit <= 0 {
		limit = 15
	}
	const q = `
		SELECT tag, COUNT(*)
		FROM books, UNNEST(tags) AS tag
		WHERE deleted_at IS NULL
		GROUP BY tag
		ORDER BY 2 DESC, tag
		LIMIT $1
	`
	return r.query(ctx, q, limit)
}

type StatsYearBucket struct {
	Decade int
	Count  int
}

// YearHistogram groups by decade (e.g. 2020 covers 2020-2029). Books
// with year = 0 (unknown) are excluded — an "unknown" bar doesn't add
// much signal.
func (r *StatsRepo) YearHistogram(ctx context.Context) ([]StatsYearBucket, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT (year / 10) * 10 AS decade, COUNT(*)
		FROM books
		WHERE deleted_at IS NULL AND year > 0
		GROUP BY decade
		ORDER BY decade
	`)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, func(s scanner) (StatsYearBucket, error) {
		var b StatsYearBucket
		err := s.Scan(&b.Decade, &b.Count)
		return b, err
	})
}

type StatsRatingBucket struct {
	Rating int
	Count  int
}

// RatingDistribution buckets by the stored integer rating (1..5).
// Zero-rated books (unrated) are excluded so the chart only shows
// opinions the user has expressed.
func (r *StatsRepo) RatingDistribution(ctx context.Context) ([]StatsRatingBucket, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT rating, COUNT(*)
		FROM books
		WHERE deleted_at IS NULL AND rating > 0
		GROUP BY rating
		ORDER BY rating
	`)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, func(s scanner) (StatsRatingBucket, error) {
		var b StatsRatingBucket
		err := s.Scan(&b.Rating, &b.Count)
		return b, err
	})
}

// UserProgressCounts returns a (reading, finished) pair for the user.
// "Reading" = progress 1..99; "Finished" = progress >= 100.
func (r *StatsRepo) UserProgressCounts(ctx context.Context, userID string) (reading, finished int, _ error) {
	// COUNT(*) FILTER returns 0 (not NULL) over an empty row set, so both
	// columns scan straight into int.
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE progress BETWEEN 1 AND 99),
			COUNT(*) FILTER (WHERE progress >= 100)
		FROM user_book_progress
		WHERE user_id = $1
	`
	err := r.db.SQL.QueryRowContext(ctx, q, userID).Scan(&reading, &finished)
	return reading, finished, err
}

// UserAnnotationCount returns the user's total annotation count.
func (r *StatsRepo) UserAnnotationCount(ctx context.Context, userID string) (int, error) {
	const q = `SELECT COUNT(*) FROM annotations WHERE user_id = $1`
	var n int
	err := r.db.SQL.QueryRowContext(ctx, q, userID).Scan(&n)
	return n, err
}

// UserShelfCounts returns (total, smart) shelf counts for the user.
func (r *StatsRepo) UserShelfCounts(ctx context.Context, userID string) (total, smart int, _ error) {
	const q = `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE is_smart)
		FROM shelves
		WHERE user_id = $1
	`
	err := r.db.SQL.QueryRowContext(ctx, q, userID).Scan(&total, &smart)
	return total, smart, err
}

// query is the shared scan path for the label/count buckets. Kept
// private; callers reach in via the typed public methods.
func (r *StatsRepo) query(ctx context.Context, sql string, args ...any) ([]StatsBucket, error) {
	rows, err := r.db.SQL.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, func(s scanner) (StatsBucket, error) {
		var b StatsBucket
		err := s.Scan(&b.Label, &b.Count)
		return b, err
	})
}
