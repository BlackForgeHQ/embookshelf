package repo

import (
	"context"

	"github.com/blackforge/embookshelf/internal/db"
)

type ProgressRepo struct {
	db *db.DB
}

func NewProgressRepo(db *db.DB) *ProgressRepo {
	return &ProgressRepo{db: db}
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
