package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

type SessionRepo struct {
	pool *pgxpool.Pool
}

func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo {
	return &SessionRepo{pool: pool}
}

func (r *SessionRepo) Create(ctx context.Context, userID, userAgent string, ttl time.Duration) (model.Session, error) {
	var s model.Session
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, expires_at, user_agent)
		VALUES ($1, now() + $2::interval, $3)
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`, userID, ttl.String(), userAgent).Scan(
		&s.ID, &s.UserID, &s.ExpiresAt, &s.UserAgent, &s.CreatedAt, &s.LastUsedAt,
	)
	return s, err
}

// GetActive returns the session and its user when the session id is valid and
// not expired, and updates last_used_at to slide the session forward. Expired
// or missing rows return ErrNotFound.
func (r *SessionRepo) GetActive(ctx context.Context, id string) (model.Session, model.User, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE sessions
		SET last_used_at = now()
		WHERE id = $1 AND expires_at > now()
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`, id)
	var s model.Session
	if err := row.Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.UserAgent, &s.CreatedAt, &s.LastUsedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s, model.User{}, ErrNotFound
		}
		return s, model.User{}, err
	}

	userRow := r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, s.UserID)
	u, err := scanUser(userRow)
	if err != nil {
		return s, u, err
	}
	return s, u, nil
}

// Extend pushes the session's expires_at forward.
func (r *SessionRepo) Extend(ctx context.Context, id string, ttl time.Duration) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE sessions
		SET expires_at = now() + $2::interval
		WHERE id = $1
	`, id, ttl.String())
	return err
}

// Delete removes a single session (used by logout).
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

// PurgeExpired removes all expired sessions; called opportunistically at boot.
func (r *SessionRepo) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
