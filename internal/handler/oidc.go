package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
)

const (
	oidcStateCookie = "embookshelf_oidc_state"
	oidcNonceCookie = "embookshelf_oidc_nonce"
)

// OIDCConfig tells the SPA whether the OIDC login button should be shown.
func (h *Handler) OIDCConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled": h.oidc != nil,
	})
}

// OIDCLogin redirects the browser to the OIDC provider's authorization endpoint.
func (h *Handler) OIDCLogin(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusNotFound, "OIDC is not configured")
		return
	}

	authURL, state, nonce, err := h.oidc.AuthURL()
	if err != nil {
		writeServerError(c, "oidc auth url", err)
		return
	}

	// Persist state + nonce in short-lived, HttpOnly cookies so the callback
	// can verify them. 10 minutes should be more than enough.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, state, 600, "/", "", h.Secure(), true)
	c.SetCookie(oidcNonceCookie, nonce, 600, "/", "", h.Secure(), true)

	c.Redirect(http.StatusFound, authURL)
}

// OIDCCallback handles the redirect back from the OIDC provider.
func (h *Handler) OIDCCallback(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusNotFound, "OIDC is not configured")
		return
	}

	// Verify state.
	state, err := c.Cookie(oidcStateCookie)
	if err != nil || state == "" || state != c.Query("state") {
		writeError(c, http.StatusBadRequest, "invalid OIDC state — please try again")
		return
	}
	nonce, err := c.Cookie(oidcNonceCookie)
	if err != nil || nonce == "" {
		writeError(c, http.StatusBadRequest, "missing OIDC nonce — please try again")
		return
	}

	// Clear the one-time cookies.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, "", -1, "/", "", h.Secure(), true)
	c.SetCookie(oidcNonceCookie, "", -1, "/", "", h.Secure(), true)

	// Check for upstream error.
	if errParam := c.Query("error"); errParam != "" {
		desc := c.Query("error_description")
		if desc == "" {
			desc = errParam
		}
		writeError(c, http.StatusBadRequest, "OIDC provider error: "+desc)
		return
	}

	code := c.Query("code")
	if code == "" {
		writeError(c, http.StatusBadRequest, "missing authorization code")
		return
	}

	sess, _, err := h.oidc.Exchange(c.Request.Context(), code, nonce, c.Request.UserAgent())
	if err != nil {
		writeServerError(c, "oidc exchange", err)
		return
	}

	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())

	// Redirect to the SPA — the session cookie is set, so /me will succeed.
	c.Redirect(http.StatusFound, "/")
}
