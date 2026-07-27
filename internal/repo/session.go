// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

type SessionRepo struct {
	db *db.DB
}

func NewSessionRepo(d *db.DB) *SessionRepo {
	return &SessionRepo{db: d}
}

func (r *SessionRepo) Create(ctx context.Context, userID, userAgent string, ttl time.Duration) (model.Session, error) {
	const q = `
		INSERT INTO sessions (user_id, expires_at, user_agent)
		VALUES ($1, now() + $2::interval, $3)
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`
	var s model.Session
	err := r.db.SQL.QueryRowContext(ctx, q, userID, ttl.String(), userAgent).Scan(
		&s.ID, &s.UserID, &s.ExpiresAt, &s.UserAgent, &s.CreatedAt, &s.LastUsedAt,
	)
	if err != nil {
		return s, err
	}
	return s, nil
}

// GetActive returns the session and its user when the session id is valid and
// not expired, and updates last_used_at to slide the session forward. Expired
// or missing rows return ErrNotFound.
func (r *SessionRepo) GetActive(ctx context.Context, id string) (model.Session, model.User, error) {
	const q = `
		UPDATE sessions
		SET last_used_at = now()
		WHERE id = $1 AND expires_at > now()
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`

	var s model.Session
	row := r.db.SQL.QueryRowContext(ctx, q, id)
	if err := row.Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.UserAgent, &s.CreatedAt, &s.LastUsedAt); err != nil {
		if dberr.IsNotFound(err) {
			return s, model.User{}, ErrNotFound
		}
		return s, model.User{}, err
	}

	const uq = `SELECT ` + userCols + ` FROM users WHERE id = $1`
	userRow := r.db.SQL.QueryRowContext(ctx, uq, s.UserID)
	u, err := scanUser(userRow)
	if err != nil {
		return s, u, err
	}
	return s, u, nil
}

// Extend pushes the session's expires_at forward.
func (r *SessionRepo) Extend(ctx context.Context, id string, ttl time.Duration) error {
	const q = `
		UPDATE sessions
		SET expires_at = now() + $2::interval
		WHERE id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, ttl.String())
	return err
}

// Delete removes a single session (used by logout).
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM sessions WHERE id = $1`
	_, err := r.db.SQL.ExecContext(ctx, q, id)
	return err
}

// DeleteForUser removes every session belonging to a user and reports how
// many went. Called after a password changes — by reset or by the account
// page — so that a session established with the old password stops working,
// which is the whole point of resetting a compromised account.
func (r *SessionRepo) DeleteForUser(ctx context.Context, userID string) (int64, error) {
	const q = `DELETE FROM sessions WHERE user_id = $1`
	res, err := r.db.SQL.ExecContext(ctx, q, userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// PurgeExpired removes all expired sessions; called opportunistically at boot.
func (r *SessionRepo) PurgeExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM sessions WHERE expires_at <= now()`
	res, err := r.db.SQL.ExecContext(ctx, q)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
