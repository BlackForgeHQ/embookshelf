// SPDX-License-Identifier: AGPL-3.0-or-later

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

type UserRepo struct {
	db *db.DB
}

func NewUserRepo(d *db.DB) *UserRepo {
	return &UserRepo{db: d}
}

const userCols = `id, email, password_hash, name, role, avatar_url, status, status_changed_at, created_at, updated_at, last_seen_at, kindle_email`

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (model.User, error) {
	const q = `
		SELECT ` + userCols + `
		FROM users
		WHERE lower(email) = lower($1)
	`
	row := r.db.SQL.QueryRowContext(ctx, q, strings.TrimSpace(email))
	return r.scanUser(row)
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (model.User, error) {
	const q = `SELECT ` + userCols + ` FROM users WHERE id = $1`
	row := r.db.SQL.QueryRowContext(ctx, q, id)
	return r.scanUser(row)
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (r *UserRepo) Create(ctx context.Context, email, name, hash string, role model.Role) (model.User, error) {
	id := db.NewID()
	const q = `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES ($1, lower($2), $3, $4, $5)
		RETURNING ` + userCols
	row := r.db.SQL.QueryRowContext(ctx, q,
		id, strings.TrimSpace(email), strings.TrimSpace(name), hash, string(role))
	return r.scanUser(row)
}

func (r *UserRepo) TouchLastSeen(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE users SET last_seen_at = $2 WHERE id = $1`
	_, err := r.db.SQL.ExecContext(ctx, q, id, at)
	return err
}

func (r *UserRepo) List(ctx context.Context) ([]model.User, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT `+userCols+`
		FROM users
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.User
	for rows.Next() {
		u, err := r.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePassword replaces the stored hash. Callers verify the old password first.
func (r *UserRepo) UpdatePassword(ctx context.Context, id, hash string) error {
	const q = `UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`
	res, err := r.db.SQL.ExecContext(ctx, q, hash, id)
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

// UpdateRole flips admin/user. The caller is responsible for preventing the
// last admin from demoting themselves — enforced at the service layer.
func (r *UserRepo) UpdateRole(ctx context.Context, id string, role model.Role) error {
	const q = `UPDATE users SET role = $1, updated_at = now() WHERE id = $2`
	res, err := r.db.SQL.ExecContext(ctx, q, string(role), id)
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

// UpdateKindleEmail sets or clears the Send-to-Kindle target for the
// caller. Empty string clears the column. The handler validates the
// `^[a-z0-9._-]+@kindle\.com$` shape — this method is shape-agnostic.
// ADR-0021.
func (r *UserRepo) UpdateKindleEmail(ctx context.Context, id, email string) error {
	const q = `UPDATE users SET kindle_email = $1, updated_at = now() WHERE id = $2`
	res, err := r.db.SQL.ExecContext(ctx, q, strings.TrimSpace(email), id)
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

// UpdateName is used by a user to edit their own display name.
func (r *UserRepo) UpdateName(ctx context.Context, id, name string) error {
	const q = `UPDATE users SET name = $1, updated_at = now() WHERE id = $2`
	res, err := r.db.SQL.ExecContext(ctx, q, strings.TrimSpace(name), id)
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

func (r *UserRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = $1`
	res, err := r.db.SQL.ExecContext(ctx, q, id)
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

// CountByRole returns how many active users hold the given role. Used to
// refuse the last-admin demotion / delete / deny path. Pending and denied
// admins do not count — only an active admin can sign in and recover the
// instance, so the guard tracks active admins specifically.
func (r *UserRepo) CountByRole(ctx context.Context, role model.Role) (int, error) {
	const q = `SELECT count(*) FROM users WHERE role = $1 AND status = 'active'`
	var n int
	err := r.db.SQL.QueryRowContext(ctx, q, string(role)).Scan(&n)
	return n, err
}

// CreateOIDC creates a user provisioned via OIDC (no local password).
// The OIDC identity is inserted separately into user_identities by
// the service layer; the users row only carries the profile fields.
func (r *UserRepo) CreateOIDC(ctx context.Context, email, name string, role model.Role) (model.User, error) {
	id := db.NewID()
	const q = `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES ($1, lower($2), $3, NULL, $4)
		RETURNING ` + userCols
	row := r.db.SQL.QueryRowContext(ctx, q,
		id, strings.TrimSpace(email), strings.TrimSpace(name), string(role))
	return r.scanUser(row)
}

// CreateOIDCPending mirrors CreateOIDC but inserts the user with
// status='pending' so they cannot log in until an admin approves them.
func (r *UserRepo) CreateOIDCPending(ctx context.Context, email, name string, role model.Role) (model.User, error) {
	id := db.NewID()
	const q = `
		INSERT INTO users (id, email, name, password_hash, role, status, status_changed_at)
		VALUES ($1, lower($2), $3, NULL, $4, 'pending', now())
		RETURNING ` + userCols
	row := r.db.SQL.QueryRowContext(ctx, q,
		id, strings.TrimSpace(email), strings.TrimSpace(name), string(role))
	return r.scanUser(row)
}

// UpdateStatus flips the approval status. The caller (service) enforces
// guards (last admin, self-target) before calling this.
func (r *UserRepo) UpdateStatus(ctx context.Context, id string, status model.UserStatus) error {
	const q = `
		UPDATE users
		SET status = $1, status_changed_at = now(), updated_at = now()
		WHERE id = $2
	`
	res, err := r.db.SQL.ExecContext(ctx, q, string(status), id)
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

// SyncOIDCProfile keeps name and avatar in line with the provider on every
// login. Empty strings in `name` or `avatarURL` leave the column untouched so
// a provider that stops supplying a claim doesn't wipe out a user-edited name.
func (r *UserRepo) SyncOIDCProfile(ctx context.Context, userID, name, avatarURL string) error {
	const q = `
		UPDATE users
		SET
		    name       = CASE WHEN $2 = '' THEN name       ELSE $2 END,
		    avatar_url = CASE WHEN $3 = '' THEN avatar_url ELSE $3 END,
		    updated_at = now()
		WHERE id = $1
	`
	n := strings.TrimSpace(name)
	a := strings.TrimSpace(avatarURL)
	_, err := r.db.SQL.ExecContext(ctx, q, userID, n, a)
	return err
}

func (r *UserRepo) scanUser(s scanner) (model.User, error) {
	return scanUser(s)
}

// scanUser is the package-level scanner used by both UserRepo and SessionRepo
// (which needs to re-hydrate the user after fetching an active session).
func scanUser(s scanner) (model.User, error) {
	var (
		u      model.User
		role   string
		status string
	)
	var passwordHash *string
	err := s.Scan(
		&u.ID, &u.Email, &passwordHash, &u.Name, &role,
		&u.AvatarURL,
		&status, &u.StatusChangedAt,
		&u.CreatedAt, &u.UpdatedAt, &u.LastSeenAt,
		&u.KindleEmail,
	)
	if passwordHash != nil {
		u.PasswordHash = *passwordHash
	}
	if err != nil {
		if dberr.IsNotFound(err) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.Role = model.Role(role)
	u.Status = model.UserStatus(status)
	return u, nil
}
