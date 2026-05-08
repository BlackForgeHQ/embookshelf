package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// UserInviteRepo persists admin-issued invitations. Lifecycle:
// Create → email goes out → invitee accepts → MarkAccepted ties the
// row to the new users row. Distinct from password reset because the
// users row doesn't exist at create time. ADR-0020.
type UserInviteRepo struct {
	db *db.DB
}

func NewUserInviteRepo(d *db.DB) *UserInviteRepo {
	return &UserInviteRepo{db: d}
}

// UserInvite is the public row shape. TokenHash is exposed only so
// admin "list pending" can show created_at; the plaintext is gone
// the moment Create returns.
type UserInvite struct {
	TokenHash  []byte
	Email      string
	Role       model.Role
	InvitedBy  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	UserID     *string
}

// Create inserts a row keyed by sha256(token). expiresAt is the
// absolute deadline; the handler picks 7d typical.
func (r *UserInviteRepo) Create(ctx context.Context, hash []byte, email string, role model.Role, invitedBy string, expiresAt time.Time) error {
	const qPG = `
		INSERT INTO user_invites (token_hash, email, role, invited_by, expires_at)
		VALUES ($1, lower($2), $3, $4, $5)
	`
	const qSQLite = `
		INSERT INTO user_invites (token_hash, email, role, invited_by, expires_at)
		VALUES (?, lower(?), ?, ?, ?)
	`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, hash, strings.TrimSpace(email), string(role), invitedBy, expiresAt.UTC().Format(time.RFC3339Nano))
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, hash, strings.TrimSpace(email), string(role), invitedBy, expiresAt.UTC())
	return err
}

// GetByHash loads the row for a hash if still usable. ErrNotFound
// when missing, expired, or already accepted — handler returns 410
// regardless to avoid leaking status to a guesser.
func (r *UserInviteRepo) GetByHash(ctx context.Context, hash []byte, now time.Time) (UserInvite, error) {
	const qPG = `
		SELECT token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id
		FROM user_invites
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > $2
	`
	const qSQLite = `
		SELECT token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id
		FROM user_invites
		WHERE token_hash = ? AND accepted_at IS NULL AND expires_at > ?
	`
	var row interface{ Scan(...any) error }
	if r.db.Dialect == db.DialectSQLite {
		row = r.db.SQL.QueryRowContext(ctx, qSQLite, hash, now.UTC().Format(time.RFC3339Nano))
	} else {
		row = r.db.SQL.QueryRowContext(ctx, qPG, hash, now.UTC())
	}
	return r.scan(row)
}

// MarkAccepted seals the invite to the new users row. One statement
// guards against double-accept if two tabs race the form.
func (r *UserInviteRepo) MarkAccepted(ctx context.Context, hash []byte, userID string, now time.Time) error {
	const qPG = `
		UPDATE user_invites
		SET accepted_at = $2, user_id = $3
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > $2
	`
	const qSQLite = `
		UPDATE user_invites
		SET accepted_at = ?, user_id = ?
		WHERE token_hash = ? AND accepted_at IS NULL AND expires_at > ?
	`
	var (
		res interface {
			RowsAffected() (int64, error)
		}
		err error
	)
	if r.db.Dialect == db.DialectSQLite {
		nowISO := now.UTC().Format(time.RFC3339Nano)
		res, err = r.db.SQL.ExecContext(ctx, qSQLite, nowISO, userID, hash, nowISO)
	} else {
		res, err = r.db.SQL.ExecContext(ctx, qPG, hash, now.UTC(), userID)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPending returns every unaccepted, unexpired invite ordered by
// most-recent-first. Drives the admin invites panel.
func (r *UserInviteRepo) ListPending(ctx context.Context, now time.Time) ([]UserInvite, error) {
	const qPG = `
		SELECT token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id
		FROM user_invites
		WHERE accepted_at IS NULL AND expires_at > $1
		ORDER BY created_at DESC
	`
	const qSQLite = `
		SELECT token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id
		FROM user_invites
		WHERE accepted_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC
	`
	var rows interface {
		Next() bool
		Scan(...any) error
		Close() error
		Err() error
	}
	var err error
	if r.db.Dialect == db.DialectSQLite {
		rows, err = r.db.SQL.QueryContext(ctx, qSQLite, now.UTC().Format(time.RFC3339Nano))
	} else {
		rows, err = r.db.SQL.QueryContext(ctx, qPG, now.UTC())
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []UserInvite
	for rows.Next() {
		inv, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// Revoke deletes a pending invite. Idempotent — no-op if the hash is
// gone (already accepted, already revoked).
func (r *UserInviteRepo) Revoke(ctx context.Context, hash []byte) error {
	const qPG = `DELETE FROM user_invites WHERE token_hash = $1 AND accepted_at IS NULL`
	const qSQLite = `DELETE FROM user_invites WHERE token_hash = ? AND accepted_at IS NULL`
	_, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), hash)
	return err
}

// PurgeExpired drops rows whose expiry is past and which never
// accepted. Sweeper fodder.
func (r *UserInviteRepo) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	const qPG = `DELETE FROM user_invites WHERE expires_at < $1 AND accepted_at IS NULL`
	const qSQLite = `DELETE FROM user_invites WHERE expires_at < ? AND accepted_at IS NULL`
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

func (r *UserInviteRepo) scan(s scanner) (UserInvite, error) {
	var (
		inv         UserInvite
		role        string
		userID      *string
		createdAny  any
		expiresAny  any
		acceptedAny any
	)
	if err := s.Scan(&inv.TokenHash, &inv.Email, &role, &inv.InvitedBy, &createdAny, &expiresAny, &acceptedAny, &userID); err != nil {
		if dberr.IsNotFound(err) {
			return UserInvite{}, ErrNotFound
		}
		return UserInvite{}, err
	}
	inv.Role = model.Role(role)
	inv.UserID = userID
	if err := db.ScanTime(r.db.Dialect, createdAny, &inv.CreatedAt); err != nil {
		return UserInvite{}, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, expiresAny, &inv.ExpiresAt); err != nil {
		return UserInvite{}, fmt.Errorf("scan expires_at: %w", err)
	}
	if err := db.ScanNullTime(r.db.Dialect, acceptedAny, &inv.AcceptedAt); err != nil {
		return UserInvite{}, fmt.Errorf("scan accepted_at: %w", err)
	}
	return inv, nil
}
