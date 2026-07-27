// SPDX-License-Identifier: AGPL-3.0-or-later

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
	const q = `SELECT ` + identityCols + ` FROM user_identities WHERE issuer = $1 AND subject = $2`
	row := r.db.SQL.QueryRowContext(ctx, q, issuer, subject)
	return r.scan(row)
}

// ListByUser returns every identity linked to the user, ordered by
// linked_at ASC so the UI shows them in the order they were attached.
func (r *IdentityRepo) ListByUser(ctx context.Context, userID string) ([]model.Identity, error) {
	const q = `SELECT ` + identityCols + ` FROM user_identities WHERE user_id = $1 ORDER BY linked_at ASC`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, r.scan)
}

// CountByUser returns how many identities are linked to a user.
func (r *IdentityRepo) CountByUser(ctx context.Context, userID string) (int, error) {
	const q = `SELECT count(*) FROM user_identities WHERE user_id = $1`
	var n int
	err := r.db.SQL.QueryRowContext(ctx, q, userID).Scan(&n)
	return n, err
}

// Insert creates a new identity row. Returns ErrIdentityForeignUser
// when (issuer, subject) is already linked to a different user, and
// dberr.IsUniqueViolation == "user_identities_user_id_provider_key"
// when the user already has another identity with the same provider
// slug. The handler maps both to 409.
func (r *IdentityRepo) Insert(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error) {
	id := db.NewID()
	now := time.Now().UTC()
	const q = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
		RETURNING ` + identityCols

	provider = strings.TrimSpace(provider)
	row := r.db.SQL.QueryRowContext(ctx, q,
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
	now := time.Now().UTC()
	id := db.NewID()
	const q = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
		ON CONFLICT (issuer, subject) DO UPDATE
		SET email         = COALESCE(EXCLUDED.email, user_identities.email),
		    last_login_at = EXCLUDED.last_login_at
		RETURNING ` + identityCols
	row := r.db.SQL.QueryRowContext(ctx, q,
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
	now := time.Now().UTC()
	const q = `
		INSERT INTO user_identities (id, user_id, provider, issuer, subject, email, linked_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
		ON CONFLICT (user_id, provider) DO UPDATE
		SET issuer        = EXCLUDED.issuer,
		    subject       = EXCLUDED.subject,
		    email         = COALESCE(EXCLUDED.email, user_identities.email),
		    last_login_at = EXCLUDED.last_login_at
		RETURNING ` + identityCols
	row := r.db.SQL.QueryRowContext(ctx, q,
		id, userID, strings.TrimSpace(provider), issuer, subject, strings.TrimSpace(email),
		now, now,
	)
	return r.scan(row)
}

// TouchLastLogin bumps last_login_at for an existing identity. Used
// by the login callback after a successful (issuer, subject) lookup.
func (r *IdentityRepo) TouchLastLogin(ctx context.Context, id string) error {
	now := time.Now().UTC()
	const q = `UPDATE user_identities SET last_login_at = $2 WHERE id = $1`
	_, err := r.db.SQL.ExecContext(ctx, q, id, now)
	return err
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
	var one int
	err := r.db.SQL.QueryRowContext(ctx, qExists, userID, provider).Scan(&one)
	if err != nil {
		if dberr.IsNotFound(err) {
			return false, ErrNotFound
		}
		return false, err
	}

	const qDel = `
		DELETE FROM user_identities
		WHERE user_id = $1 AND provider = $2
		  AND (
		      (SELECT password_hash FROM users WHERE id = $1) IS NOT NULL
		      AND (SELECT password_hash FROM users WHERE id = $1) <> ''
		      OR (SELECT count(*) FROM user_identities WHERE user_id = $1) > 1
		  )
	`
	// Zero rows means the guard clause in the statement matched — the
	// user has no other credential — not that the identity was absent.
	if err := execOne(ctx, r.db.SQL, qDel, userID, provider); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, ErrIdentityLockout
		}
		return false, err
	}
	return true, nil
}

func (r *IdentityRepo) scan(s scanner) (model.Identity, error) {
	var i model.Identity
	err := s.Scan(
		&i.ID, &i.UserID, &i.Provider, &i.Issuer, &i.Subject, &i.Email,
		&i.LinkedAt, &i.LastLoginAt,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return i, ErrNotFound
		}
		return i, err
	}
	return i, nil
}
