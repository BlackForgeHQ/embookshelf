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

const userCols = `id, email, password_hash, name, role, oidc_subject, oidc_issuer, avatar_url, status, status_changed_at, created_at, updated_at, last_seen_at`

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (model.User, error) {
	const qPG = `
		SELECT ` + userCols + `
		FROM users
		WHERE lower(email) = lower($1)
	`
	const qSQLite = `
		SELECT ` + userCols + `
		FROM users
		WHERE lower(email) = lower(?)
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), strings.TrimSpace(email))
	return r.scanUser(row)
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (model.User, error) {
	const qPG = `SELECT ` + userCols + ` FROM users WHERE id = $1`
	const qSQLite = `SELECT ` + userCols + ` FROM users WHERE id = ?`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	return r.scanUser(row)
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (r *UserRepo) Create(ctx context.Context, email, name, hash string, role model.Role) (model.User, error) {
	id := db.NewID()
	const qPG = `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES ($1, lower($2), $3, $4, $5)
		RETURNING ` + userCols
	const qSQLite = `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES (?, lower(?), ?, ?, ?)
		RETURNING ` + userCols
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, strings.TrimSpace(email), strings.TrimSpace(name), hash, string(role))
	return r.scanUser(row)
}

func (r *UserRepo) TouchLastSeen(ctx context.Context, id string, at time.Time) error {
	const qPG = `UPDATE users SET last_seen_at = $2 WHERE id = $1`
	const qSQLite = `UPDATE users SET last_seen_at = ? WHERE id = ?`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, at.UTC().Format(time.RFC3339Nano), id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, id, at)
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
	const qPG = `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`
	const qSQLite = `UPDATE users SET password_hash = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), hash, id)
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
	const qPG = `UPDATE users SET role = $2, updated_at = now() WHERE id = $1`
	const qSQLite = `UPDATE users SET role = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), string(role), id)
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
	const qPG = `UPDATE users SET name = $2, updated_at = now() WHERE id = $1`
	const qSQLite = `UPDATE users SET name = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), strings.TrimSpace(name), id)
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
	const qPG = `DELETE FROM users WHERE id = $1`
	const qSQLite = `DELETE FROM users WHERE id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
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
	const qPG = `SELECT count(*) FROM users WHERE role = $1 AND status = 'active'`
	const qSQLite = `SELECT count(*) FROM users WHERE role = ? AND status = 'active'`
	var n int
	err := r.db.SQL.QueryRowContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		string(role)).Scan(&n)
	return n, err
}

// GetByOIDC looks up a user by their OIDC issuer+subject pair.
func (r *UserRepo) GetByOIDC(ctx context.Context, issuer, subject string) (model.User, error) {
	const qPG = `
		SELECT ` + userCols + `
		FROM users
		WHERE oidc_issuer = $1 AND oidc_subject = $2
	`
	const qSQLite = `
		SELECT ` + userCols + `
		FROM users
		WHERE oidc_issuer = ? AND oidc_subject = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), issuer, subject)
	return r.scanUser(row)
}

// CreateOIDC creates a user provisioned via OIDC (no local password).
func (r *UserRepo) CreateOIDC(ctx context.Context, email, name string, role model.Role, issuer, subject string) (model.User, error) {
	id := db.NewID()
	const qPG = `
		INSERT INTO users (id, email, name, password_hash, role, oidc_issuer, oidc_subject)
		VALUES ($1, lower($2), $3, '', $4, $5, $6)
		RETURNING ` + userCols
	const qSQLite = `
		INSERT INTO users (id, email, name, password_hash, role, oidc_issuer, oidc_subject)
		VALUES (?, lower(?), ?, '', ?, ?, ?)
		RETURNING ` + userCols
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, strings.TrimSpace(email), strings.TrimSpace(name), string(role), issuer, subject)
	return r.scanUser(row)
}

// CreateOIDCPending mirrors CreateOIDC but inserts the user with
// status='pending' so they cannot log in until an admin approves them.
func (r *UserRepo) CreateOIDCPending(ctx context.Context, email, name string, role model.Role, issuer, subject string) (model.User, error) {
	id := db.NewID()
	const qPG = `
		INSERT INTO users (id, email, name, password_hash, role, oidc_issuer, oidc_subject, status, status_changed_at)
		VALUES ($1, lower($2), $3, '', $4, $5, $6, 'pending', now())
		RETURNING ` + userCols
	const qSQLite = `
		INSERT INTO users (id, email, name, password_hash, role, oidc_issuer, oidc_subject, status, status_changed_at)
		VALUES (?, lower(?), ?, '', ?, ?, ?, 'pending', (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		RETURNING ` + userCols
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, strings.TrimSpace(email), strings.TrimSpace(name), string(role), issuer, subject)
	return r.scanUser(row)
}

// UpdateStatus flips the approval status. The caller (service) enforces
// guards (last admin, self-target) before calling this.
func (r *UserRepo) UpdateStatus(ctx context.Context, id string, status model.UserStatus) error {
	const qPG = `
		UPDATE users
		SET status = $2, status_changed_at = now(), updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE users
		SET status = ?, status_changed_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), string(status), id)
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

// LinkOIDC sets the OIDC identity on an existing user (e.g. linking a
// password-based user to their SSO identity on first OIDC login).
func (r *UserRepo) LinkOIDC(ctx context.Context, userID, issuer, subject string) error {
	const qPG = `UPDATE users SET oidc_issuer = $2, oidc_subject = $3, updated_at = now() WHERE id = $1`
	const qSQLite = `UPDATE users SET oidc_issuer = ?, oidc_subject = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id = ?`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, issuer, subject, userID)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, userID, issuer, subject)
	return err
}

// SyncOIDCProfile keeps name and avatar in line with the provider on every
// login. Empty strings in `name` or `avatarURL` leave the column untouched so
// a provider that stops supplying a claim doesn't wipe out a user-edited name.
func (r *UserRepo) SyncOIDCProfile(ctx context.Context, userID, name, avatarURL string) error {
	const qPG = `
		UPDATE users
		SET
		    name       = CASE WHEN $2 = '' THEN name       ELSE $2 END,
		    avatar_url = CASE WHEN $3 = '' THEN avatar_url ELSE $3 END,
		    updated_at = now()
		WHERE id = $1
	`
	const qSQLite = `
		UPDATE users
		SET
		    name       = CASE WHEN ? = '' THEN name       ELSE ? END,
		    avatar_url = CASE WHEN ? = '' THEN avatar_url ELSE ? END,
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE id = ?
	`
	n := strings.TrimSpace(name)
	a := strings.TrimSpace(avatarURL)
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, n, n, a, a, userID)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, userID, n, a)
	return err
}

func (r *UserRepo) scanUser(s scanner) (model.User, error) {
	return scanUser(r.db.Dialect, s)
}

// scanUser is the package-level scanner used by both UserRepo and SessionRepo
// (which needs to re-hydrate the user after fetching an active session).
func scanUser(d db.Dialect, s scanner) (model.User, error) {
	var (
		u                model.User
		role             string
		status           string
		statusChangedAny any
		createdAny       any
		updatedAny       any
		lastSeenAny      any
	)
	err := s.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role,
		&u.OIDCSubject, &u.OIDCIssuer, &u.AvatarURL,
		&status, &statusChangedAny,
		&createdAny, &updatedAny, &lastSeenAny,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.Role = model.Role(role)
	u.Status = model.UserStatus(status)
	if err := db.ScanNullTime(d, statusChangedAny, &u.StatusChangedAt); err != nil {
		return u, fmt.Errorf("scan status_changed_at: %w", err)
	}
	if err := db.ScanTime(d, createdAny, &u.CreatedAt); err != nil {
		return u, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanTime(d, updatedAny, &u.UpdatedAt); err != nil {
		return u, fmt.Errorf("scan updated_at: %w", err)
	}
	if err := db.ScanNullTime(d, lastSeenAny, &u.LastSeenAt); err != nil {
		return u, fmt.Errorf("scan last_seen_at: %w", err)
	}
	return u, nil
}
