package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// ErrIdentityLockout is returned by DeleteWithGuard when removing the
// identity would leave the user with no usable credential (no other
// identity AND no password). See CONTEXT.md → "Lockout guard".
var ErrIdentityLockout = errors.New("identity: removing this identity would lock the user out; set a password or link another provider first")

// ErrIdentityForeignUser is returned by Insert when the (issuer,
// subject) pair is already linked to a different user. The handler
// translates this to HTTP 409.
var ErrIdentityForeignUser = errors.New("identity: already linked to another user")

type IdentityRepo struct {
	db *db.DB
}

func NewIdentityRepo(d *db.DB) *IdentityRepo {
	return &IdentityRepo{db: d}
}

const identityCols = `id, user_id, provider, issuer, subject, email, linked_at, last_login_at`

// GetByIssuerSubject finds an identity by the IdP-attested pair. The
// uniqueness constraint guarantees at most one row.
func (r *IdentityRepo) GetByIssuerSubject(ctx context.Context, issuer, subject string) (model.Identity, error) {
	const qPG = `SELECT ` + identityCols + ` FROM user_identities WHERE issuer = $1 AND subject = $2`
	const qSQLite = `SELECT ` + identityCols + ` FROM user_identities WHERE issuer = ? AND subject = ?`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), issuer, subject)
	return r.scan(row)
}

// ListByUser returns every identity linked to the user, ordered by
// linked_at ASC so the UI shows them in the order they were attached.
func (r *IdentityRepo) ListByUser(ctx context.Context, userID string) ([]model.Identity, error) {
	const qPG = `SELECT ` + identityCols + ` FROM user_identities WHERE user_id = $1 ORDER BY linked_at ASC`
	const qSQLite = `SELECT ` + identityCols + ` FROM user_identities WHERE user_id = ? ORDER BY linked_at ASC`
	rows, err := r.db.SQL.QueryContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Identity
	for rows.Next() {
		i, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// CountByUser returns how many identities are linked to a user.
func (r *IdentityRepo) CountByUser(ctx context.Context, userID string) (int, error) {
	const qPG = `SELECT count(*) FROM user_identities WHERE user_id = $1`
	const qSQLite = `SELECT count(*) FROM user_identities WHERE user_id = ?`
	var n int
	err := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID).Scan(&n)
	return n, err
}

// Insert creates a new identity row. Returns ErrIdentityForeignUser
// when (issuer, subject) is already linked to a different user, and
// dberr.IsUniqueViolation == "user_identities_user_id_provider_key"
// when the user already has another identity with the same provider
// slug. The handler maps both to 409.
func (r *IdentityRepo) Insert(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error) {
	id := db.NewID()
	now := timeForDialect(r.db.Dialect, time.Now().UTC())
	const qPG = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
		RETURNING ` + identityCols
	const qSQLite = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)
		RETURNING ` + identityCols

	provider = strings.TrimSpace(provider)
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, userID, provider, issuer, subject, strings.TrimSpace(email),
		now, now,
	)
	ident, err := r.scan(row)
	if err == nil {
		return ident, nil
	}
	if ok, name := dberr.IsUniqueViolation(err); ok {
		if name == "user_identities_issuer_subject_key" {
			// Pair belongs to another user.
			return model.Identity{}, ErrIdentityForeignUser
		}
		// user_identities_user_id_provider_key — caller signals
		// "provider already linked, unlink first".
		return model.Identity{}, fmt.Errorf("%w: provider %q already linked", ErrIdentityForeignUser, provider)
	}
	return model.Identity{}, err
}

// Upsert is the login-callback variant: if (issuer, subject) already
// exists for ANY user, refresh email + last_login_at without changing
// user_id. Otherwise insert a new row owned by userID. Used by the
// auto-link path on `service/oidc.go` Exchange.
func (r *IdentityRepo) Upsert(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error) {
	now := timeForDialect(r.db.Dialect, time.Now().UTC())
	id := db.NewID()
	const qPG = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
		ON CONFLICT (issuer, subject) DO UPDATE
		SET email         = COALESCE(EXCLUDED.email, user_identities.email),
		    last_login_at = EXCLUDED.last_login_at
		RETURNING ` + identityCols
	const qSQLite = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)
		ON CONFLICT (issuer, subject) DO UPDATE
		SET email         = COALESCE(excluded.email, user_identities.email),
		    last_login_at = excluded.last_login_at
		RETURNING ` + identityCols
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, userID, strings.TrimSpace(provider), issuer, subject, strings.TrimSpace(email),
		now, now,
	)
	return r.scan(row)
}

// RelinkProvider attaches an (issuer, subject) pair to userID under
// the given provider slot, replacing any existing identity in that
// slot. Used by the login email-match auto-link path so a user who
// rebinds their Google account (same email, new sub) keeps a single
// row per provider. Single-statement upsert keyed on (user_id,
// provider) so the operation is race-free.
func (r *IdentityRepo) RelinkProvider(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error) {
	id := db.NewID()
	now := timeForDialect(r.db.Dialect, time.Now().UTC())
	const qPG = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
		ON CONFLICT (user_id, provider) DO UPDATE
		SET issuer        = EXCLUDED.issuer,
		    subject       = EXCLUDED.subject,
		    email         = COALESCE(EXCLUDED.email, user_identities.email),
		    last_login_at = EXCLUDED.last_login_at
		RETURNING ` + identityCols
	const qSQLite = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)
		ON CONFLICT (user_id, provider) DO UPDATE
		SET issuer        = excluded.issuer,
		    subject       = excluded.subject,
		    email         = COALESCE(excluded.email, user_identities.email),
		    last_login_at = excluded.last_login_at
		RETURNING ` + identityCols
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, userID, strings.TrimSpace(provider), issuer, subject, strings.TrimSpace(email),
		now, now,
	)
	return r.scan(row)
}

// TouchLastLogin bumps last_login_at for an existing identity. Used
// by the login callback after a successful (issuer, subject) lookup.
func (r *IdentityRepo) TouchLastLogin(ctx context.Context, id string) error {
	now := timeForDialect(r.db.Dialect, time.Now().UTC())
	const qPG = `UPDATE user_identities SET last_login_at = $2 WHERE id = $1`
	const qSQLite = `UPDATE user_identities SET last_login_at = ? WHERE id = ?`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, now, id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, id, now)
	return err
}

// timeForDialect returns the dialect-appropriate parameter value for
// a TIMESTAMPTZ / TEXT timestamp column. PG accepts time.Time
// directly via the pgx codec; SQLite expects RFC3339Nano TEXT.
func timeForDialect(d db.Dialect, t time.Time) any {
	if d == db.DialectSQLite {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return t
}

// DeleteWithGuard removes the identity row for (userID, provider) but
// only if the user has either a password or another identity left.
// The whole check runs inside a single statement so the row count is
// race-free against concurrent unlink/set-password operations.
//
// Returns (true, nil) when the row was deleted, (false,
// ErrIdentityLockout) when the guard refused, and (false, ErrNotFound)
// when there was no such identity.
func (r *IdentityRepo) DeleteWithGuard(ctx context.Context, userID, provider string) (bool, error) {
	// We can't easily distinguish "guarded" from "missing" from a
	// single DELETE row count, so do a two-step check:
	//   1) does the row exist?
	//   2) would deletion violate the guard?
	// Both queries are read-only; the DELETE itself re-evaluates the
	// guard atomically so the gap between (1) and (3) cannot lock a
	// user out.
	const qExists = `SELECT 1 FROM user_identities WHERE user_id = $1 AND provider = $2`
	const qExistsSQLite = `SELECT 1 FROM user_identities WHERE user_id = ? AND provider = ?`
	var one int
	err := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qExists, qExistsSQLite), userID, provider).Scan(&one)
	if err != nil {
		if dberr.IsNotFound(err) {
			return false, ErrNotFound
		}
		return false, err
	}

	const qDelPG = `
		DELETE FROM user_identities
		WHERE user_id = $1 AND provider = $2
		  AND (
		      (SELECT password_hash FROM users WHERE id = $1) IS NOT NULL
		      AND (SELECT password_hash FROM users WHERE id = $1) <> ''
		      OR (SELECT count(*) FROM user_identities WHERE user_id = $1) > 1
		  )
	`
	const qDelSQLite = `
		DELETE FROM user_identities
		WHERE user_id = ? AND provider = ?
		  AND (
		      (SELECT password_hash FROM users WHERE id = ?) IS NOT NULL
		      AND (SELECT password_hash FROM users WHERE id = ?) <> ''
		      OR (SELECT count(*) FROM user_identities WHERE user_id = ?) > 1
		  )
	`
	var res interface{ RowsAffected() (int64, error) }
	if r.db.Dialect == db.DialectSQLite {
		r2, err := r.db.SQL.ExecContext(ctx, qDelSQLite, userID, provider, userID, userID, userID)
		if err != nil {
			return false, err
		}
		res = r2
	} else {
		r2, err := r.db.SQL.ExecContext(ctx, qDelPG, userID, provider)
		if err != nil {
			return false, err
		}
		res = r2
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, ErrIdentityLockout
	}
	return true, nil
}

func (r *IdentityRepo) scan(s scanner) (model.Identity, error) {
	var (
		i              model.Identity
		linkedAtAny    any
		lastLoginAtAny any
	)
	err := s.Scan(
		&i.ID, &i.UserID, &i.Provider, &i.Issuer, &i.Subject, &i.Email,
		&linkedAtAny, &lastLoginAtAny,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return i, ErrNotFound
		}
		return i, err
	}
	if err := db.ScanTime(r.db.Dialect, linkedAtAny, &i.LinkedAt); err != nil {
		return i, fmt.Errorf("scan linked_at: %w", err)
	}
	if err := db.ScanNullTime(r.db.Dialect, lastLoginAtAny, &i.LastLoginAt); err != nil {
		return i, fmt.Errorf("scan last_login_at: %w", err)
	}
	return i, nil
}
