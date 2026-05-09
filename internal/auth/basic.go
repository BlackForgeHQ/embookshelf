// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

// Verifier is the slice of AuthService that the Basic Auth middleware needs.
// Service defines it; we redeclare the interface here to keep this file
// import-free of the service package.
type Verifier interface {
	Verify(ctx context.Context, email, password string) (model.User, error)
}

// BasicAuth verifies HTTP Basic credentials on every request and stashes the
// authenticated user in context. Intended for stateless clients (OPDS
// e-readers) that can't juggle session cookies.
func BasicAuth(v Verifier, realm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Basic ") {
			challenge(c, realm)
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
		if err != nil {
			challenge(c, realm)
			return
		}
		creds := strings.SplitN(string(decoded), ":", 2)
		if len(creds) != 2 {
			challenge(c, realm)
			return
		}
		user, err := v.Verify(c.Request.Context(), creds[0], creds[1])
		if err != nil {
			challenge(c, realm)
			return
		}
		c.Request = c.Request.WithContext(WithUser(c.Request.Context(), &user))
		c.Next()
	}
}

func challenge(c *gin.Context, realm string) {
	c.Header("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
	c.AbortWithStatus(http.StatusUnauthorized)
}
