package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
)

// OIDCConfig returns the public list of enabled providers — the login
// page uses this to render one "Sign in with …" button per provider.
// Safe to serve unauthenticated: no secrets.
func (h *Handler) OIDCConfig(c *gin.Context) {
	if h.oidc == nil {
		c.JSON(http.StatusOK, gin.H{"providers": []any{}, "forceOnly": false})
		return
	}
	cfg, err := h.oidc.PublicConfig(c.Request.Context())
	if err != nil {
		writeServerError(c, "oidc public config", err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// OIDCLogin initiates the flow for the provider slug in the URL.
func (h *Handler) OIDCLogin(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusNotFound, "OIDC is not configured")
		return
	}
	slug := c.Param("slug")
	authURL, err := h.oidc.AuthURL(c.Request.Context(), slug, requestOrigin(c))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOIDCUnknownProvider):
			writeError(c, http.StatusNotFound, "unknown provider")
		case errors.Is(err, service.ErrOIDCDisabled), errors.Is(err, service.ErrOIDCNotConfigured):
			writeError(c, http.StatusNotFound, "provider is not enabled")
		default:
			writeServerError(c, "oidc auth url", err)
		}
		return
	}
	c.Redirect(http.StatusFound, authURL)
}

// OIDCCallback trades the authorization code for a session. Routing by
// provider lives inside the service — the state token carries the slug.
func (h *Handler) OIDCCallback(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusNotFound, "OIDC is not configured")
		return
	}
	if errParam := c.Query("error"); errParam != "" {
		desc := c.Query("error_description")
		if desc == "" {
			desc = errParam
		}
		c.Redirect(http.StatusFound, "/login?oidcError="+errParam+"&oidcDesc="+desc)
		return
	}
	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		c.Redirect(http.StatusFound, "/login?oidcError=invalidRequest")
		return
	}

	sess, _, err := h.oidc.Exchange(c.Request.Context(), code, state, c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, service.ErrOIDCPendingApproval) {
			c.Redirect(http.StatusFound, "/login/pending")
			return
		}
		c.Redirect(http.StatusFound, "/login?oidcError="+oidcErrorCode(err))
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.Redirect(http.StatusFound, "/")
}

// requestOrigin returns "scheme://host" inferred from the incoming
// request. Honors X-Forwarded-Proto and X-Forwarded-Host so reverse-
// proxy deployments that forgot to set APP_URL still get a usable
// redirect_uri. Used as a fallback when cfg.AppURL is empty.
func requestOrigin(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if xf := c.GetHeader("X-Forwarded-Proto"); xf != "" {
		// Proto header can be comma-separated; first hop wins.
		if i := strings.IndexByte(xf, ','); i >= 0 {
			xf = xf[:i]
		}
		scheme = strings.TrimSpace(xf)
	}
	host := c.Request.Host
	if xh := c.GetHeader("X-Forwarded-Host"); xh != "" {
		if i := strings.IndexByte(xh, ','); i >= 0 {
			xh = xh[:i]
		}
		host = strings.TrimSpace(xh)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func oidcErrorCode(err error) string {
	switch {
	case errors.Is(err, service.ErrOIDCStateMismatch):
		return "stateMismatch"
	case errors.Is(err, service.ErrOIDCLoginNotAllowed):
		return "userNotProvisioned"
	case errors.Is(err, service.ErrOIDCDisabled):
		return "disabled"
	case errors.Is(err, service.ErrOIDCNotConfigured):
		return "notConfigured"
	case errors.Is(err, service.ErrOIDCUnknownProvider):
		return "notConfigured"
	case errors.Is(err, service.ErrOIDCPendingApproval):
		return "pendingApproval"
	default:
		return "unknown"
	}
}
