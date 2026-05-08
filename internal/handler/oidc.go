package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
)

// OIDCConfig returns the public list of enabled providers + the
// forward-auth notice flag — the login page uses this to render one
// "Sign in with …" button per provider and to swap the local form
// for an SSO-only notice when the deployment is gated by an upstream
// proxy. Safe to serve unauthenticated: no secrets.
func (h *Handler) OIDCConfig(c *gin.Context) {
	resp := gin.H{
		"providers":            []any{},
		"forceOnly":            false,
		"forwardAuthEnabled":   false,
		"hideLocalLogin":       false,
		"forwardAuthLogoutUrl": "",
	}
	if h.oidc != nil {
		cfg, err := h.oidc.PublicConfig(c.Request.Context())
		if err != nil {
			writeServerError(c, "oidc public config", err)
			return
		}
		resp["providers"] = cfg.Providers
		resp["forceOnly"] = cfg.ForceOnly
	}
	if h.fwdAuthHolder != nil {
		if fa := h.fwdAuthHolder.Get(); fa != nil && fa.Enabled {
			resp["forwardAuthEnabled"] = true
			resp["hideLocalLogin"] = fa.HideLocalForm
			resp["forwardAuthLogoutUrl"] = fa.LogoutURL
		}
	}
	c.JSON(http.StatusOK, resp)
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

// OIDCCallback trades the authorization code for a session (login
// flow) or attaches an identity to the signed-in user (link flow).
// The state token carries the intent + provider slug; the service
// dispatches based on those and returns a discriminated outcome.
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

	// Pull the current session user (if any) so link-intent flows can
	// verify the callback fired in the same browser that initiated the
	// link. Login-intent flows ignore this and always issue a session.
	var sessionUserID string
	if u := auth.UserFromContext(c.Request.Context()); u != nil {
		sessionUserID = u.ID
	}

	out, err := h.oidc.Exchange(c.Request.Context(), code, state, c.Request.UserAgent(), sessionUserID)
	if err != nil {
		if errors.Is(err, service.ErrOIDCPendingApproval) {
			c.Redirect(http.StatusFound, "/login/pending")
			return
		}
		// Link-intent errors land back on /account so the panel can
		// render a toast next to the row the user just touched.
		if errors.Is(err, service.ErrOIDCLinkUserMismatch) {
			c.Redirect(http.StatusFound, "/account?error=session_expired")
			return
		}
		c.Redirect(http.StatusFound, "/login?oidcError="+oidcErrorCode(err))
		return
	}
	if out.Intent == service.IntentLink {
		c.Redirect(http.StatusFound, "/account?linked="+out.Provider)
		return
	}
	auth.SetSessionCookie(c, out.Session.ID, service.SessionTTL, h.Secure())
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
