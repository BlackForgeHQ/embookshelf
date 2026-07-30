// SPDX-License-Identifier: AGPL-3.0-or-later

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

// sessionProjection is the sessions row, declared once. Both RETURNING
// clauses and the Scan destinations render from here.
//
// Three of the six columns are TIMESTAMPTZ — expires_at, created_at and
// last_used_at — which is the Column-order coupling hazard on the auth
// surface: crossing a session's creation time with its expiry compiles,
// runs, and reads downstream as an expiry bug rather than as a wrong
// column. Stating a column's position and its destination in one value
// makes that swap unrepresentable.
var sessionProjection = projection[model.Session]{
	{name: "id", dest: func(s *model.Session) any { return &s.ID }},
	{name: "user_id", dest: func(s *model.Session) any { return &s.UserID }},
	{name: "expires_at", dest: func(s *model.Session) any { return &s.ExpiresAt }},
	{name: "user_agent", dest: func(s *model.Session) any { return &s.UserAgent }},
	{name: "created_at", dest: func(s *model.Session) any { return &s.CreatedAt }},
	{name: "last_used_at", dest: func(s *model.Session) any { return &s.LastUsedAt }},
}

// sessionReturning renders the projection with no alias in scope: both
// queries that read a session row are RETURNING clauses, which have no
// FROM to alias the table.
var sessionReturning = sessionProjection.returningList("sessions")

func (r *SessionRepo) Create(ctx context.Context, userID, userAgent string, ttl time.Duration) (model.Session, error) {
	// The INSERT's own column list stays outside the projection: it
	// names the insertable subset (the three defaulted columns are not
	// in it), which is a different membership question. Create's
	// round-trip test guards it.
	q := `
		INSERT INTO sessions (user_id, expires_at, user_agent)
		VALUES ($1, now() + $2::interval, $3)
		RETURNING ` + sessionReturning
	row := r.db.SQL.QueryRowContext(ctx, q, userID, ttl.String(), userAgent)
	return scanSession(row)
}

// GetActive returns the session and its user when the session id is valid and
// not expired, and updates last_used_at to slide the session forward. Expired
// or missing rows return ErrNotFound.
func (r *SessionRepo) GetActive(ctx context.Context, id string) (model.Session, model.User, error) {
	q := `
		UPDATE sessions
		SET last_used_at = now()
		WHERE id = $1 AND expires_at > now()
		RETURNING ` + sessionReturning

	row := r.db.SQL.QueryRowContext(ctx, q, id)
	s, err := scanSession(row)
	if err != nil {
		if dberr.IsNotFound(err) {
			return s, model.User{}, ErrNotFound
		}
		return s, model.User{}, err
	}

	const uq = `SELECT ` + userCols + ` FROM users WHERE id = $1`
	userRow := r.db.SQL.QueryRowContext(ctx, uq, s.UserID)
	u, err := scanUser(userRow)
	if err != nil {
		return s, u, err
	}
	return s, u, nil
}

// Extend pushes the session's expires_at forward.
func (r *SessionRepo) Extend(ctx context.Context, id string, ttl time.Duration) error {
	const q = `
		UPDATE sessions
		SET expires_at = now() + $2::interval
		WHERE id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, ttl.String())
	return err
}

// Delete removes a single session (used by logout).
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM sessions WHERE id = $1`
	_, err := r.db.SQL.ExecContext(ctx, q, id)
	return err
}

// DeleteForUser removes every session belonging to a user and reports how
// many went. Called after a password changes — by reset or by the account
// page — so that a session established with the old password stops working,
// which is the whole point of resetting a compromised account.
func (r *SessionRepo) DeleteForUser(ctx context.Context, userID string) (int64, error) {
	const q = `DELETE FROM sessions WHERE user_id = $1`
	res, err := r.db.SQL.ExecContext(ctx, q, userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// scanSession hydrates a row into the model shape, taking its
// destinations from the projection in the order it declares.
//
// The raw error is returned rather than mapped to ErrNotFound: Create's
// INSERT ... RETURNING always produces a row, and GetActive is the only
// caller for which "no row" means "expired or gone", so it does the
// mapping itself.
func scanSession(s scanner) (model.Session, error) {
	var sess model.Session
	err := sessionProjection.scan(s, &sess)
	return sess, err
}

// PurgeExpired removes all expired sessions; called opportunistically at boot.
func (r *SessionRepo) PurgeExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM sessions WHERE expires_at <= now()`
	res, err := r.db.SQL.ExecContext(ctx, q)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
