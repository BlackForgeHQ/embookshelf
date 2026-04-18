package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ProgressRepo struct {
	pool *pgxpool.Pool
}

func NewProgressRepo(pool *pgxpool.Pool) *ProgressRepo {
	return &ProgressRepo{pool: pool}
}

// Set records a user's progress for a book. The CFI argument is optional; an
// empty string leaves any previously-stored resume point intact (so a manual
// percent tweak doesn't wipe out a reader-recorded CFI).
func (r *ProgressRepo) Set(ctx context.Context, userID, bookID string, progress int, cfi string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_book_progress (user_id, book_id, progress, resume_cfi, last_read_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, book_id) DO UPDATE
		  SET progress     = EXCLUDED.progress,
		      resume_cfi   = CASE WHEN EXCLUDED.resume_cfi <> ''
		                          THEN EXCLUDED.resume_cfi
		                          ELSE user_book_progress.resume_cfi END,
		      last_read_at = now()
	`, userID, bookID, progress, cfi)
	return err
}

// Clear removes a user's progress for a book (equivalent to "mark unread").
func (r *ProgressRepo) Clear(ctx context.Context, userID, bookID string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM user_book_progress WHERE user_id = $1 AND book_id = $2
	`, userID, bookID)
	return err
}
