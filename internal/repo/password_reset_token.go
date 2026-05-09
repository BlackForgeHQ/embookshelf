// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
)

// PasswordResetTokenRepo persists single-use reset tokens. Plaintext
// tokens never enter this layer — callers hash with sha256 before
// Create / Consume. ADR-0020.
type PasswordResetTokenRepo struct {
	db *db.DB
}

func NewPasswordResetTokenRepo(d *db.DB) *PasswordResetTokenRepo {
	return &PasswordResetTokenRepo{db: d}
}

// PasswordResetToken is the row shape; UserID is the only field
// callers need beyond the lifecycle status.
type PasswordResetToken struct {
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// Create inserts a fresh row. Hash is the sha256 of the plaintext
// token (32 bytes); expiry is the absolute deadline. The plaintext
// is the caller's responsibility to mail and forget.
func (r *PasswordResetTokenRepo) Create(ctx context.Context, hash []byte, userID string, expiresAt time.Time) error {
	const qPG = `
		INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)
	`
	const qSQLite = `
		INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
		VALUES (?, ?, ?)
	`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, hash, userID, expiresAt.UTC().Format(time.RFC3339Nano))
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, hash, userID, expiresAt.UTC())
	return err
}

// Consume atomically marks a token used and returns its row. Returns
// ErrNotFound when the hash is unknown, expired, or already
// consumed; the caller cannot distinguish, which is the point — every
// failure mode looks the same to a brute-force attempt. The whole
// path is one statement so a concurrent double-submit can only land
// once.
func (r *PasswordResetTokenRepo) Consume(ctx context.Context, hash []byte, now time.Time) (PasswordResetToken, error) {
	const qPG = `
		UPDATE password_reset_tokens
		SET used_at = $2
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND expires_at > $2
		RETURNING user_id, created_at, expires_at, used_at
	`
	const qSQLite = `
		UPDATE password_reset_tokens
		SET used_at = ?
		WHERE token_hash = ?
		  AND used_at IS NULL
		  AND expires_at > ?
		RETURNING user_id, created_at, expires_at, used_at
	`
	var (
		t          PasswordResetToken
		createdAny any
		expiresAny any
		usedAny    any
		nowISO     = now.UTC().Format(time.RFC3339Nano)
	)
	var row interface{ Scan(...any) error }
	if r.db.Dialect == db.DialectSQLite {
		row = r.db.SQL.QueryRowContext(ctx, qSQLite, nowISO, hash, nowISO)
	} else {
		row = r.db.SQL.QueryRowContext(ctx, qPG, hash, now.UTC())
	}
	if err := row.Scan(&t.UserID, &createdAny, &expiresAny, &usedAny); err != nil {
		if dberr.IsNotFound(err) {
			return PasswordResetToken{}, ErrNotFound
		}
		return PasswordResetToken{}, err
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &t.CreatedAt); err != nil {
		return PasswordResetToken{}, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, expiresAny, &t.ExpiresAt); err != nil {
		return PasswordResetToken{}, fmt.Errorf("scan expires_at: %w", err)
	}
	if err := db.ScanNullTime(r.db.Dialect, usedAny, &t.UsedAt); err != nil {
		return PasswordResetToken{}, fmt.Errorf("scan used_at: %w", err)
	}
	return t, nil
}

// Verify returns the row for a hash if it is still usable (not used,
// not expired). Used by the GET /verify pre-flight so the UI can show
// "link expired" before the user types a new password. Read-only —
// does not mark used.
func (r *PasswordResetTokenRepo) Verify(ctx context.Context, hash []byte, now time.Time) (PasswordResetToken, error) {
	const qPG = `
		SELECT user_id, created_at, expires_at, used_at
		FROM password_reset_tokens
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
	`
	const qSQLite = `
		SELECT user_id, created_at, expires_at, used_at
		FROM password_reset_tokens
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
	`
	var (
		t          PasswordResetToken
		createdAny any
		expiresAny any
		usedAny    any
	)
	var row interface{ Scan(...any) error }
	if r.db.Dialect == db.DialectSQLite {
		row = r.db.SQL.QueryRowContext(ctx, qSQLite, hash, now.UTC().Format(time.RFC3339Nano))
	} else {
		row = r.db.SQL.QueryRowContext(ctx, qPG, hash, now.UTC())
	}
	if err := row.Scan(&t.UserID, &createdAny, &expiresAny, &usedAny); err != nil {
		if dberr.IsNotFound(err) {
			return PasswordResetToken{}, ErrNotFound
		}
		return PasswordResetToken{}, err
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &t.CreatedAt); err != nil {
		return PasswordResetToken{}, err
	}
	if err := db.ScanTime(r.db.Dialect, expiresAny, &t.ExpiresAt); err != nil {
		return PasswordResetToken{}, err
	}
	if err := db.ScanNullTime(r.db.Dialect, usedAny, &t.UsedAt); err != nil {
		return PasswordResetToken{}, err
	}
	return t, nil
}

// CountRecentForUser counts unused, non-expired tokens issued for a
// user within the window. Drives the per-user "1 reset email per 5
// minutes" rate limit so a noisy attacker can't spam a target's
// inbox.
func (r *PasswordResetTokenRepo) CountRecentForUser(ctx context.Context, userID string, since time.Time) (int, error) {
	const qPG = `
		SELECT count(*) FROM password_reset_tokens
		WHERE user_id = $1 AND created_at >= $2
	`
	const qSQLite = `
		SELECT count(*) FROM password_reset_tokens
		WHERE user_id = ? AND created_at >= ?
	`
	var n int
	if r.db.Dialect == db.DialectSQLite {
		err := r.db.SQL.QueryRowContext(ctx, qSQLite, userID, since.UTC().Format(time.RFC3339Nano)).Scan(&n)
		return n, err
	}
	err := r.db.SQL.QueryRowContext(ctx, qPG, userID, since.UTC()).Scan(&n)
	return n, err
}

// PurgeExpired drops every row whose expiry is past. Hourly sweeper
// fodder; the audit value of an expired-and-unused row decays fast.
func (r *PasswordResetTokenRepo) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	const qPG = `DELETE FROM password_reset_tokens WHERE expires_at < $1`
	const qSQLite = `DELETE FROM password_reset_tokens WHERE expires_at < ?`
	var arg any = now.UTC()
	if r.db.Dialect == db.DialectSQLite {
		arg = now.UTC().Format(time.RFC3339Nano)
	}
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), arg)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
