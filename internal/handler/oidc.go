package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
)

// OIDCConfig returns the public subset of OIDC settings — the login page
// uses this to render "Sign in with {provider}" and to honor force-only
// mode. Rendered even when OIDC is off so the SPA can decide layout.
func (h *Handler) OIDCConfig(c *gin.Context) {
	if h.oidc == nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "forceOnly": false, "configured": false})
		return
	}
	cfg, err := h.oidc.PublicConfig(c.Request.Context())
	if err != nil {
		writeServerError(c, "oidc public config", err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// OIDCLogin generates a PKCE challenge + state and redirects the browser
// to the provider's authorize endpoint. State is held server-side — no
// cookies needed for the round trip.
func (h *Handler) OIDCLogin(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusNotFound, "OIDC is not configured")
		return
	}
	authURL, _, err := h.oidc.AuthURL(c.Request.Context())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOIDCDisabled), errors.Is(err, service.ErrOIDCNotConfigured):
			writeError(c, http.StatusNotFound, "OIDC is not configured")
		default:
			writeServerError(c, "oidc auth url", err)
		}
		return
	}
	c.Redirect(http.StatusFound, authURL)
}

// OIDCCallback trades the authorization code for a BookLore session and
// redirects to the dashboard. Failures redirect to /login with an error
// query param so the SPA can render a friendly message.
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
		code := oidcErrorCode(err)
		c.Redirect(http.StatusFound, "/login?oidcError="+code)
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.Redirect(http.StatusFound, "/")
}

// oidcErrorCode maps service errors to the stable short codes the SPA
// translates for display.
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
	default:
		return "unknown"
	}
}
