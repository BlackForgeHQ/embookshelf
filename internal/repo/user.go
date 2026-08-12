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

type UserRepo struct {
	db *db.DB
}

func NewUserRepo(d *db.DB) *UserRepo {
	return &UserRepo{db: d}
}

// userProjection is the users row, declared once — SELECTs, RETURNING
// clauses and the Scan destinations all render from here. The list used
// to leak across a file boundary: SessionRepo.GetActive re-hydrates the
// user through the same projection, so the two files can no longer
// agree only by hand. Six of the twelve columns are TEXT, the
// Column-order coupling hazard's densest run on the auth surface.
var userProjection = projection[model.User]{
	{name: "id", dest: func(u *model.User) any { return &u.ID }},
	{name: "email", dest: func(u *model.User) any { return &u.Email }},
	// password_hash is NULL for OIDC-provisioned users; the model keeps
	// a plain string, "" meaning "no local password".
	{name: "password_hash", dest: func(u *model.User) any { return nullText{Dst: &u.PasswordHash} }},
	{name: "name", dest: func(u *model.User) any { return &u.Name }},
	{name: "role", dest: func(u *model.User) any { return &u.Role }},
	{name: "avatar_url", dest: func(u *model.User) any { return &u.AvatarURL }},
	{name: "status", dest: func(u *model.User) any { return &u.Status }},
	{name: "status_changed_at", dest: func(u *model.User) any { return &u.StatusChangedAt }},
	{name: "created_at", dest: func(u *model.User) any { return &u.CreatedAt }},
	{name: "updated_at", dest: func(u *model.User) any { return &u.UpdatedAt }},
	{name: "last_seen_at", dest: func(u *model.User) any { return &u.LastSeenAt }},
	{name: "kindle_email", dest: func(u *model.User) any { return &u.KindleEmail }},
}

var (
	userCols      = userProjection.selectList("users")
	userReturning = userProjection.returningList("users")
)

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (model.User, error) {
	q := `
		SELECT ` + userCols + `
		FROM users
		WHERE lower(email) = lower($1)
	`
	row := r.db.SQL.QueryRowContext(ctx, q, strings.TrimSpace(email))
	return r.scanUser(row)
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (model.User, error) {
	q := `SELECT ` + userCols + ` FROM users WHERE id = $1`
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
	q := `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES ($1, lower($2), $3, $4, $5)
		RETURNING ` + userReturning
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
	return collect(rows, nil, r.scanUser)
}

// UpdatePassword replaces the stored hash. Callers verify the old password first.
func (r *UserRepo) UpdatePassword(ctx context.Context, id, hash string) error {
	const q = `UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`
	return execOne(ctx, r.db.SQL, q, hash, id)
}

// UpdateRole flips admin/user. The caller is responsible for preventing the
// last admin from demoting themselves — enforced at the service layer.
func (r *UserRepo) UpdateRole(ctx context.Context, id string, role model.Role) error {
	const q = `UPDATE users SET role = $1, updated_at = now() WHERE id = $2`
	return execOne(ctx, r.db.SQL, q, string(role), id)
}

// UpdateKindleEmail sets or clears the Send-to-Kindle target for the
// caller. Empty string clears the column. The handler validates the
// `^[a-z0-9._-]+@kindle\.com$` shape — this method is shape-agnostic.
// ADR-0021.
func (r *UserRepo) UpdateKindleEmail(ctx context.Context, id, email string) error {
	const q = `UPDATE users SET kindle_email = $1, updated_at = now() WHERE id = $2`
	return execOne(ctx, r.db.SQL, q, strings.TrimSpace(email), id)
}

// UpdateName is used by a user to edit their own display name.
func (r *UserRepo) UpdateName(ctx context.Context, id, name string) error {
	const q = `UPDATE users SET name = $1, updated_at = now() WHERE id = $2`
	return execOne(ctx, r.db.SQL, q, strings.TrimSpace(name), id)
}

func (r *UserRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = $1`
	return execOne(ctx, r.db.SQL, q, id)
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
	q := `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES ($1, lower($2), $3, NULL, $4)
		RETURNING ` + userReturning
	row := r.db.SQL.QueryRowContext(ctx, q,
		id, strings.TrimSpace(email), strings.TrimSpace(name), string(role))
	return r.scanUser(row)
}

// CreateOIDCPending mirrors CreateOIDC but inserts the user with
// status='pending' so they cannot log in until an admin approves them.
func (r *UserRepo) CreateOIDCPending(ctx context.Context, email, name string, role model.Role) (model.User, error) {
	id := db.NewID()
	q := `
		INSERT INTO users (id, email, name, password_hash, role, status, status_changed_at)
		VALUES ($1, lower($2), $3, NULL, $4, 'pending', now())
		RETURNING ` + userReturning
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
	return execOne(ctx, r.db.SQL, q, string(status), id)
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
// (which needs to re-hydrate the user after fetching an active session). Its
// destinations come from the projection, in the order it declares — the
// enum columns settle into their named string types directly, and the
// nullable password_hash lands through the nullText adapter.
func scanUser(s scanner) (model.User, error) {
	var u model.User
	if err := userProjection.scan(s, &u); err != nil {
		if dberr.IsNotFound(err) {
			return u, ErrNotFound
		}
		return u, err
	}
	return u, nil
}
