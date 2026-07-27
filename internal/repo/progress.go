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
	const q = `SELECT MAX(last_read_at) FROM user_book_progress WHERE book_id = $1`

	// MAX() over no rows is SQL NULL, so the destination has to be
	// nullable even though the column is not: a book nobody has opened
	// yields the zero time rather than an error.
	var latest *time.Time
	err := r.db.SQL.QueryRowContext(ctx, q, bookID).Scan(&latest)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	if latest == nil {
		return time.Time{}, nil
	}
	return *latest, nil
}

// Set records a user's progress for a book. The CFI argument is optional; an
// empty string leaves any previously-stored resume point intact (so a manual
// percent tweak doesn't wipe out a reader-recorded CFI).
func (r *ProgressRepo) Set(ctx context.Context, userID, bookID string, progress int, cfi string) error {
	const q = `
		INSERT INTO user_book_progress (user_id, book_id, progress, resume_cfi, last_read_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, book_id) DO UPDATE
		  SET progress     = EXCLUDED.progress,
		      resume_cfi   = CASE WHEN EXCLUDED.resume_cfi <> ''
		                          THEN EXCLUDED.resume_cfi
		                          ELSE user_book_progress.resume_cfi END,
		      last_read_at = now()
	`
	_, err := r.db.SQL.ExecContext(ctx, q, userID, bookID, progress, cfi)
	return err
}
