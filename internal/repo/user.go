package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

const userCols = `id, email, password_hash, name, role, oidc_subject, oidc_issuer, avatar_url, status, status_changed_at, created_at, updated_at, last_seen_at`

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+userCols+`
		FROM users
		WHERE lower(email) = lower($1)
	`, strings.TrimSpace(email))
	return scanUser(row)
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (r *UserRepo) Create(ctx context.Context, email, name, hash string, role model.Role) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES (lower($1), $2, $3, $4)
		RETURNING `+userCols+`
	`, strings.TrimSpace(email), strings.TrimSpace(name), hash, string(role))
	return scanUser(row)
}

func (r *UserRepo) TouchLastSeen(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET last_seen_at = $2 WHERE id = $1`, id, at)
	return err
}

func (r *UserRepo) List(ctx context.Context) ([]model.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userCols+`
		FROM users
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePassword replaces the stored hash. Callers verify the old password first.
func (r *UserRepo) UpdatePassword(ctx context.Context, id, hash string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRole flips admin/user. The caller is responsible for preventing the
// last admin from demoting themselves — enforced at the service layer.
func (r *UserRepo) UpdateRole(ctx context.Context, id string, role model.Role) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET role = $2, updated_at = now() WHERE id = $1`, id, string(role))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateName is used by a user to edit their own display name.
func (r *UserRepo) UpdateName(ctx context.Context, id, name string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET name = $2, updated_at = now() WHERE id = $1`, id, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *UserRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountByRole returns how many active users hold the given role. Used to
// refuse the last-admin demotion / delete / deny path. Pending and denied
// admins do not count — only an active admin can sign in and recover the
// instance, so the guard tracks active admins specifically.
func (r *UserRepo) CountByRole(ctx context.Context, role model.Role) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE role = $1 AND status = 'active'`,
		string(role)).Scan(&n)
	return n, err
}

// GetByOIDC looks up a user by their OIDC issuer+subject pair.
func (r *UserRepo) GetByOIDC(ctx context.Context, issuer, subject string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+userCols+`
		FROM users
		WHERE oidc_issuer = $1 AND oidc_subject = $2
	`, issuer, subject)
	return scanUser(row)
}

// CreateOIDC creates a user provisioned via OIDC (no local password).
func (r *UserRepo) CreateOIDC(ctx context.Context, email, name string, role model.Role, issuer, subject string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, oidc_issuer, oidc_subject)
		VALUES (lower($1), $2, '', $3, $4, $5)
		RETURNING `+userCols+`
	`, strings.TrimSpace(email), strings.TrimSpace(name), string(role), issuer, subject)
	return scanUser(row)
}

// CreateOIDCPending mirrors CreateOIDC but inserts the user with
// status='pending' so they cannot log in until an admin approves them.
func (r *UserRepo) CreateOIDCPending(ctx context.Context, email, name string, role model.Role, issuer, subject string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, oidc_issuer, oidc_subject, status, status_changed_at)
		VALUES (lower($1), $2, '', $3, $4, $5, 'pending', now())
		RETURNING `+userCols+`
	`, strings.TrimSpace(email), strings.TrimSpace(name), string(role), issuer, subject)
	return scanUser(row)
}

// UpdateStatus flips the approval status. The caller (service) enforces
// guards (last admin, self-target) before calling this.
func (r *UserRepo) UpdateStatus(ctx context.Context, id string, status model.UserStatus) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users
		SET status = $2, status_changed_at = now(), updated_at = now()
		WHERE id = $1
	`, id, string(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LinkOIDC sets the OIDC identity on an existing user (e.g. linking a
// password-based user to their SSO identity on first OIDC login).
func (r *UserRepo) LinkOIDC(ctx context.Context, userID, issuer, subject string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET oidc_issuer = $2, oidc_subject = $3, updated_at = now() WHERE id = $1`,
		userID, issuer, subject)
	return err
}

// SyncOIDCProfile keeps name and avatar in line with the provider on every
// login. Empty strings in `name` or `avatarURL` leave the column untouched so
// a provider that stops supplying a claim doesn't wipe out a user-edited name.
func (r *UserRepo) SyncOIDCProfile(ctx context.Context, userID, name, avatarURL string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users
		SET
		    name       = CASE WHEN $2 = '' THEN name       ELSE $2 END,
		    avatar_url = CASE WHEN $3 = '' THEN avatar_url ELSE $3 END,
		    updated_at = now()
		WHERE id = $1
	`, userID, strings.TrimSpace(name), strings.TrimSpace(avatarURL))
	return err
}

func scanUser(s scanner) (model.User, error) {
	var (
		u      model.User
		role   string
		status string
	)
	err := s.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role,
		&u.OIDCSubject, &u.OIDCIssuer, &u.AvatarURL,
		&status, &u.StatusChangedAt,
		&u.CreatedAt, &u.UpdatedAt, &u.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.Role = model.Role(role)
	u.Status = model.UserStatus(status)
	return u, nil
}
