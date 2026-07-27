// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
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
	const q = `
		INSERT INTO user_invites (token_hash, email, role, invited_by, expires_at)
		VALUES ($1, lower($2), $3, $4, $5)
	`
	_, err := r.db.SQL.ExecContext(ctx, q, hash, strings.TrimSpace(email), string(role), invitedBy, expiresAt.UTC())
	return err
}

// GetByHash loads the row for a hash if still usable. ErrNotFound
// when missing, expired, or already accepted — handler returns 410
// regardless to avoid leaking status to a guesser.
func (r *UserInviteRepo) GetByHash(ctx context.Context, hash []byte, now time.Time) (UserInvite, error) {
	const q = `
		SELECT token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id
		FROM user_invites
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > $2
	`
	row := r.db.SQL.QueryRowContext(ctx, q, hash, now.UTC())
	return r.scan(row)
}

// MarkAccepted seals the invite to the new users row. One statement
// guards against double-accept if two tabs race the form.
func (r *UserInviteRepo) MarkAccepted(ctx context.Context, hash []byte, userID string, now time.Time) error {
	const q = `
		UPDATE user_invites
		SET accepted_at = $2, user_id = $3
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > $2
	`
	return execOne(ctx, r.db.SQL, q, hash, now.UTC(), userID)
}

// ListPending returns every unaccepted, unexpired invite ordered by
// most-recent-first. Drives the admin invites panel.
func (r *UserInviteRepo) ListPending(ctx context.Context, now time.Time) ([]UserInvite, error) {
	const q = `
		SELECT token_hash, email, role, invited_by, created_at, expires_at, accepted_at, user_id
		FROM user_invites
		WHERE accepted_at IS NULL AND expires_at > $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, now.UTC())
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, r.scan)
}

// Revoke deletes a pending invite. Idempotent — no-op if the hash is
// gone (already accepted, already revoked).
func (r *UserInviteRepo) Revoke(ctx context.Context, hash []byte) error {
	const q = `DELETE FROM user_invites WHERE token_hash = $1 AND accepted_at IS NULL`
	_, err := r.db.SQL.ExecContext(ctx, q, hash)
	return err
}

// PurgeExpired drops rows whose expiry is past and which never
// accepted. Sweeper fodder.
func (r *UserInviteRepo) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	const q = `DELETE FROM user_invites WHERE expires_at < $1 AND accepted_at IS NULL`
	res, err := r.db.SQL.ExecContext(ctx, q, now.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *UserInviteRepo) scan(s scanner) (UserInvite, error) {
	var (
		inv    UserInvite
		role   string
		userID *string
	)
	if err := s.Scan(&inv.TokenHash, &inv.Email, &role, &inv.InvitedBy, &inv.CreatedAt, &inv.ExpiresAt, &inv.AcceptedAt, &userID); err != nil {
		if dberr.IsNotFound(err) {
			return UserInvite{}, ErrNotFound
		}
		return UserInvite{}, err
	}
	inv.Role = model.Role(role)
	inv.UserID = userID
	return inv, nil
}
