// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

// ErrInvalidToken is a TokenResolver's refusal: the presented token does
// not authenticate anyone. Any other error is the resolver failing, not
// the token.
var ErrInvalidToken = errors.New("invalid or expired token")

// TokenResolver is the slice of APITokenService the bearer middleware
// needs. Declared here so this package stays free of the service package.
type TokenResolver interface {
	UserByToken(ctx context.Context, raw string) (model.User, error)
}

// BearerToken authenticates a request by its `Authorization: Bearer`
// personal access token (ADR-0039) and attaches the owner to the context.
//
// It reads nothing else — no session cookie, no forward-auth identity —
// which is half of why the route it guards may skip CSRFGuard: a bearer
// header is never attached by a browser on its own, so there is no
// ambient credential for a cross-site request to ride.
func BearerToken(resolver TokenResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerCredential(c.GetHeader("Authorization"))
		if !ok {
			bearerChallenge(c)
			return
		}
		u, err := resolver.UserByToken(c.Request.Context(), raw)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				bearerChallenge(c)
				return
			}
			// A database outage is not a bad token; answering 401 would
			// tell the client to throw away a credential that works.
			slog.Error("bearer token resolve", "err", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, unauthorizedBody{Error: "internal error"})
			return
		}
		c.Request = c.Request.WithContext(WithUser(c.Request.Context(), &u))
		c.Next()
	}
}

// bearerCredential extracts the token from an Authorization header. The
// scheme is case-insensitive (RFC 9110 §11.1); the token is not.
func bearerCredential(header string) (string, bool) {
	scheme, token, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func bearerChallenge(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="embookshelf"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, unauthorizedBody{Error: "authentication required"})
}
