// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
)

type ProgressRepo struct {
	db *db.DB
}

func NewProgressRepo(db *db.DB) *ProgressRepo {
	return &ProgressRepo{db: db}
}

// LatestForBook returns the most recent last_read_at across all users
// for the given bookID. Returns a zero time.Time when no progress rows
// exist (book never opened).
func (r *ProgressRepo) LatestForBook(ctx context.Context, bookID string) (time.Time, error) {
	const qPG = `SELECT MAX(last_read_at) FROM user_book_progress WHERE book_id = $1`
	const qSQLite = `SELECT MAX(last_read_at) FROM user_book_progress WHERE book_id = ?`

	var raw any
	err := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), bookID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	if raw == nil {
		// No rows matched — book has never been read.
		return time.Time{}, nil
	}

	var t time.Time
	if err := db.ScanTime(r.db.Dialect, raw, &t); err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// Set records a user's progress for a book. The CFI argument is optional; an
// empty string leaves any previously-stored resume point intact (so a manual
// percent tweak doesn't wipe out a reader-recorded CFI).
func (r *ProgressRepo) Set(ctx context.Context, userID, bookID string, progress int, cfi string) error {
	const qPG = `
		INSERT INTO user_book_progress (user_id, book_id, progress, resume_cfi, last_read_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, book_id) DO UPDATE
		  SET progress     = EXCLUDED.progress,
		      resume_cfi   = CASE WHEN EXCLUDED.resume_cfi <> ''
		                          THEN EXCLUDED.resume_cfi
		                          ELSE user_book_progress.resume_cfi END,
		      last_read_at = now()
	`
	const qSQLite = `
		INSERT INTO user_book_progress (user_id, book_id, progress, resume_cfi, last_read_at)
		VALUES (?, ?, ?, ?, (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (user_id, book_id) DO UPDATE
		  SET progress     = EXCLUDED.progress,
		      resume_cfi   = CASE WHEN EXCLUDED.resume_cfi <> ''
		                          THEN EXCLUDED.resume_cfi
		                          ELSE user_book_progress.resume_cfi END,
		      last_read_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		userID, bookID, progress, cfi)
	return err
}
