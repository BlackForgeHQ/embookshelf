package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// unauthorizedBody mirrors the shape the handler package writes for errors.
// Kept inline (not imported) so this package stays free of handler deps.
type unauthorizedBody struct {
	Error string `json:"error"`
}

// SessionCookieName is the cookie that carries the session ID.
const SessionCookieName = "embookshelf_session"

// SessionResolver is the minimal interface the middleware needs. *service.AuthService satisfies it.
type SessionResolver interface {
	UserBySession(ctx context.Context, sessionID string) (model.User, error)
}

// RequireAuth ensures a valid session exists and attaches the current user
// to the request context. On failure the JSON API gets a plain 401 — the
// React SPA decides how to react (typically `navigate('/login?next=...')`).
func RequireAuth(resolver SessionResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(SessionCookieName)
		if err != nil || cookie == "" {
			unauthorized(c)
			return
		}
		u, err := resolver.UserBySession(c.Request.Context(), cookie)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				ClearSessionCookie(c)
			}
			unauthorized(c)
			return
		}
		c.Request = c.Request.WithContext(WithUser(c.Request.Context(), &u))
		c.Next()
	}
}

// RequireRole gates a subroute on a role. Callers should always chain after
// RequireAuth — an unauthenticated request hits 403 here otherwise.
func RequireRole(roles ...model.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := UserFromContext(c.Request.Context())
		if u == nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		for _, r := range roles {
			if u.Role == r {
				c.Next()
				return
			}
		}
		c.AbortWithStatus(http.StatusForbidden)
	}
}

// CSRFGuard rejects state-changing requests whose Origin/Referer does not
// match the request Host. This is the Lax-SameSite-cookie + origin-check
// pattern — sufficient for HTML forms and HTMX posts on the same origin.
func CSRFGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		origin := c.Request.Header.Get("Origin")
		if origin == "" {
			origin = c.Request.Header.Get("Referer")
		}
		if origin == "" {
			// Missing Origin and Referer → likely CSRF (legit browsers send one).
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing origin"})
			return
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "bad origin"})
			return
		}
		if !strings.EqualFold(u.Host, c.Request.Host) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin mismatch"})
			return
		}
		c.Next()
	}
}

// SetSessionCookie writes the session cookie with httponly + samesite=Lax.
// The `secure` flag should be true whenever the app is served over HTTPS; in
// local dev over plain HTTP we leave it off so the cookie is actually set.
func SetSessionCookie(c *gin.Context, sessionID string, ttl time.Duration, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, sessionID, int(ttl.Seconds()), "/", "", secure, true)
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, "", -1, "/", "", false, true)
}

func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, unauthorizedBody{Error: "authentication required"})
}
