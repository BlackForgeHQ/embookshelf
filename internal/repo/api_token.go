// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// APITokenRepo persists personal access tokens (ADR-0039). Rows are keyed
// by id for management and looked up by sha256(token) for
// authentication; the plaintext never reaches this tier.
type APITokenRepo struct {
	db *db.DB
}

func NewAPITokenRepo(d *db.DB) *APITokenRepo {
	return &APITokenRepo{db: d}
}

// APITokenWithOwner is a token row plus the owner columns the admin list
// shows beside it.
type APITokenWithOwner struct {
	model.APIToken
	OwnerEmail string
	OwnerName  string
}

// apiTokenProjection is the api_tokens row, declared once. token_hash is
// deliberately absent: nothing above this tier has a use for it, and a
// hash that is never read cannot leak into a response.
//
// Three columns are TIMESTAMPTZ (created_at, last_used_at, expires_at) —
// the Column-order coupling hazard: crossing a token's last use with its
// expiry compiles and reads downstream as an auth bug.
var apiTokenProjection = projection[model.APIToken]{
	{name: "id", dest: func(t *model.APIToken) any { return &t.ID }},
	{name: "user_id", dest: func(t *model.APIToken) any { return &t.UserID }},
	{name: "name", dest: func(t *model.APIToken) any { return &t.Name }},
	{name: "prefix", dest: func(t *model.APIToken) any { return &t.Prefix }},
	{name: "created_at", dest: func(t *model.APIToken) any { return &t.CreatedAt }},
	{name: "last_used_at", dest: func(t *model.APIToken) any { return &t.LastUsedAt }},
	{name: "expires_at", dest: func(t *model.APIToken) any { return &t.ExpiresAt }},
}

// apiTokenOwnerProjection is the token projection followed by the two
// owner columns, joined in as "u". Derived from apiTokenProjection rather
// than restated, so the two lists cannot disagree about the token half.
var apiTokenOwnerProjection = func() projection[APITokenWithOwner] {
	out := make(projection[APITokenWithOwner], 0, len(apiTokenProjection)+2)
	for _, c := range apiTokenProjection {
		dest := c.dest
		out = append(out, column[APITokenWithOwner]{
			name: c.name,
			expr: c.expr,
			dest: func(o *APITokenWithOwner) any { return dest(&o.APIToken) },
		})
	}
	return append(out,
		column[APITokenWithOwner]{name: "owner_email", expr: "u.email",
			dest: func(o *APITokenWithOwner) any { return &o.OwnerEmail }},
		column[APITokenWithOwner]{name: "owner_name", expr: "u.name",
			dest: func(o *APITokenWithOwner) any { return &o.OwnerName }},
	)
}()

var (
	apiTokenReturning = apiTokenProjection.returningList("api_tokens")
	apiTokenCols      = apiTokenProjection.selectList("t")
	apiTokenOwnerCols = apiTokenOwnerProjection.selectList("t")
)

// Create inserts a token row. expiresAt nil means never.
func (r *APITokenRepo) Create(ctx context.Context, userID, name string, hash []byte, prefix string, expiresAt *time.Time) (model.APIToken, error) {
	q := `
		INSERT INTO api_tokens (user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + apiTokenReturning
	var exp any
	if expiresAt != nil {
		exp = expiresAt.UTC()
	}
	row := r.db.SQL.QueryRowContext(ctx, q, userID, name, hash, prefix, exp)
	return scanAPIToken(row)
}

// ListForUser returns a user's tokens, newest first.
func (r *APITokenRepo) ListForUser(ctx context.Context, userID string) ([]model.APIToken, error) {
	q := `
		SELECT ` + apiTokenCols + `
		FROM api_tokens t
		WHERE t.user_id = $1
		ORDER BY t.created_at DESC
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, []model.APIToken{}, scanAPIToken)
}

// ListAll returns every user's tokens with their owners, newest first.
// Drives the admin MCP panel.
func (r *APITokenRepo) ListAll(ctx context.Context) ([]APITokenWithOwner, error) {
	q := `
		SELECT ` + apiTokenOwnerCols + `
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		ORDER BY t.created_at DESC
	`
	rows, err := r.db.SQL.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return collect(rows, []APITokenWithOwner{}, func(s scanner) (APITokenWithOwner, error) {
		var o APITokenWithOwner
		err := apiTokenOwnerProjection.scan(s, &o)
		return o, err
	})
}

// GetActiveByHash loads the token for a hash if it has not expired.
// ErrNotFound for unknown and expired alike — the caller answers 401
// either way.
func (r *APITokenRepo) GetActiveByHash(ctx context.Context, hash []byte, now time.Time) (model.APIToken, error) {
	q := `
		SELECT ` + apiTokenCols + `
		FROM api_tokens t
		WHERE t.token_hash = $1 AND (t.expires_at IS NULL OR t.expires_at > $2)
	`
	row := r.db.SQL.QueryRowContext(ctx, q, hash, now.UTC())
	return scanAPIToken(row)
}

// Touch records a use, at most once a minute per token, so a chatty
// client does not turn every request into a write.
func (r *APITokenRepo) Touch(ctx context.Context, id string, now time.Time) error {
	const q = `
		UPDATE api_tokens
		SET last_used_at = $2
		WHERE id = $1
		  AND (last_used_at IS NULL OR last_used_at < $2::timestamptz - interval '1 minute')
	`
	_, err := r.db.SQL.ExecContext(ctx, q, id, now.UTC())
	return err
}

// DeleteForUser revokes one of a user's own tokens. ErrNotFound when the
// id is unknown or belongs to someone else — the two are deliberately
// indistinguishable to the caller.
func (r *APITokenRepo) DeleteForUser(ctx context.Context, userID, id string) error {
	const q = `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`
	return execOne(ctx, r.db.SQL, q, id, userID)
}

// Delete revokes any token by id. Admin path.
func (r *APITokenRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM api_tokens WHERE id = $1`
	return execOne(ctx, r.db.SQL, q, id)
}

func scanAPIToken(s scanner) (model.APIToken, error) {
	var t model.APIToken
	if err := apiTokenProjection.scan(s, &t); err != nil {
		if dberr.IsNotFound(err) {
			return model.APIToken{}, ErrNotFound
		}
		return model.APIToken{}, err
	}
	return t, nil
}
