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
	// SQLite doesn't support interval arithmetic, so we compute the expiry app-side.
	const qPG = `
		INSERT INTO sessions (user_id, expires_at, user_agent)
		VALUES ($1, now() + $2::interval, $3)
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`
	const qSQLite = `
		INSERT INTO sessions (id, user_id, expires_at, user_agent)
		VALUES (?, ?, ?, ?)
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`
	var s model.Session
	var expiresAny, createdAny, lastUsedAny any
	if r.db.Dialect == db.DialectSQLite {
		id := db.NewID()
		expiresAt := time.Now().UTC().Add(ttl).Format(time.RFC3339Nano)
		err := r.db.SQL.QueryRowContext(ctx, qSQLite, id, userID, expiresAt, userAgent).Scan(
			&s.ID, &s.UserID, &expiresAny, &s.UserAgent, &createdAny, &lastUsedAny,
		)
		if err != nil {
			return s, err
		}
		if err := db.ScanTime(r.db.Dialect, expiresAny, &s.ExpiresAt); err != nil {
			return s, fmt.Errorf("scan expires_at: %w", err)
		}
		if err := db.ScanTime(r.db.Dialect, createdAny, &s.CreatedAt); err != nil {
			return s, fmt.Errorf("scan created_at: %w", err)
		}
		if err := db.ScanTime(r.db.Dialect, lastUsedAny, &s.LastUsedAt); err != nil {
			return s, fmt.Errorf("scan last_used_at: %w", err)
		}
		return s, nil
	}
	err := r.db.SQL.QueryRowContext(ctx, qPG, userID, ttl.String(), userAgent).Scan(
		&s.ID, &s.UserID, &expiresAny, &s.UserAgent, &createdAny, &lastUsedAny,
	)
	if err != nil {
		return s, err
	}
	if err := db.ScanTime(r.db.Dialect, expiresAny, &s.ExpiresAt); err != nil {
		return s, fmt.Errorf("scan expires_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &s.CreatedAt); err != nil {
		return s, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, lastUsedAny, &s.LastUsedAt); err != nil {
		return s, fmt.Errorf("scan last_used_at: %w", err)
	}
	return s, nil
}

// GetActive returns the session and its user when the session id is valid and
// not expired, and updates last_used_at to slide the session forward. Expired
// or missing rows return ErrNotFound.
func (r *SessionRepo) GetActive(ctx context.Context, id string) (model.Session, model.User, error) {
	// SQLite doesn't support UPDATE ... RETURNING with now() comparison in
	// WHERE, so we use two statements wrapped in a transaction.
	const qPG = `
		UPDATE sessions
		SET last_used_at = now()
		WHERE id = $1 AND expires_at > now()
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`
	const qSQLite = `
		UPDATE sessions
		SET last_used_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ? AND expires_at > (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		RETURNING id, user_id, expires_at, user_agent, created_at, last_used_at
	`

	var s model.Session
	var expiresAny, createdAny, lastUsedAny any
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	if err := row.Scan(&s.ID, &s.UserID, &expiresAny, &s.UserAgent, &createdAny, &lastUsedAny); err != nil {
		if dberr.IsNotFound(err) {
			return s, model.User{}, ErrNotFound
		}
		return s, model.User{}, err
	}
	if err := db.ScanTime(r.db.Dialect, expiresAny, &s.ExpiresAt); err != nil {
		return s, model.User{}, fmt.Errorf("scan expires_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &s.CreatedAt); err != nil {
		return s, model.User{}, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, lastUsedAny, &s.LastUsedAt); err != nil {
		return s, model.User{}, fmt.Errorf("scan last_used_at: %w", err)
	}

	const uqPG = `SELECT ` + userCols + ` FROM users WHERE id = $1`
	const uqSQLite = `SELECT ` + userCols + ` FROM users WHERE id = ?`
	userRow := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, uqPG, uqSQLite), s.UserID)
	u, err := scanUser(r.db.Dialect, userRow)
	if err != nil {
		return s, u, err
	}
	return s, u, nil
}

// Extend pushes the session's expires_at forward.
func (r *SessionRepo) Extend(ctx context.Context, id string, ttl time.Duration) error {
	const qPG = `
		UPDATE sessions
		SET expires_at = now() + $2::interval
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE sessions
		SET expires_at = ?
		WHERE id = ?
	`
	if r.db.Dialect == db.DialectSQLite {
		expiresAt := time.Now().UTC().Add(ttl).Format(time.RFC3339Nano)
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, expiresAt, id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, id, ttl.String())
	return err
}

// Delete removes a single session (used by logout).
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	const qPG = `DELETE FROM sessions WHERE id = $1`
	const qSQLite = `DELETE FROM sessions WHERE id = ?`
	_, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	return err
}

// PurgeExpired removes all expired sessions; called opportunistically at boot.
func (r *SessionRepo) PurgeExpired(ctx context.Context) (int64, error) {
	const qPG = `DELETE FROM sessions WHERE expires_at <= now()`
	const qSQLite = `DELETE FROM sessions WHERE expires_at <= (strftime('%Y-%m-%dT%H:%M:%fZ','now'))`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
