// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth contains the session-based authentication primitives:
// password hashing, session cookie management, Gin middleware, and the
// request-scoped "current user" helper used throughout handlers.
package auth

import (
	"context"

	"github.com/blackforge/embookshelf/internal/model"
)

type ctxKey int

const userCtxKey ctxKey = iota

// WithUser returns a child context that carries the authenticated user.
func WithUser(ctx context.Context, u *model.User) context.Context {
	if u == nil {
		return ctx
	}
	return context.WithValue(ctx, userCtxKey, u)
}

// UserFromContext returns the authenticated user attached to the context
// (nil when the request is unauthenticated).
func UserFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(userCtxKey).(*model.User)
	return u
}
