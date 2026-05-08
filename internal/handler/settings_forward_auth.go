package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/repo"
)

// forwardAuthSettingsDTO is the admin-edit shape for the FORWARD_AUTH
// row. Mirrors repo.ForwardAuthConfig 1:1 — no secrets, so no
// "set/unset" flags like settings_oidc.go uses for client secrets.
// ADR-0022.
type forwardAuthSettingsDTO struct {
	Enabled           bool                    `json:"enabled"`
	TrustedProxyCIDRs []string                `json:"trustedProxyCIDRs"`
	Headers           repo.ForwardAuthHeaders `json:"headers"`
	LogoutURL         string                  `json:"logoutUrl"`
	HideLocalLogin    bool                    `json:"hideLocalLogin"`
}

// SettingsForwardAuthGet returns the persisted FORWARD_AUTH row.
func (h *Handler) SettingsForwardAuthGet(c *gin.Context) {
	if h.appSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return
	}
	cfg, err := h.appSettings.GetForwardAuth(c.Request.Context())
	if err != nil {
		writeServerError(c, "forward_auth settings get", err)
		return
	}
	c.JSON(http.StatusOK, forwardAuthSettingsDTO{
		Enabled:           cfg.Enabled,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		Headers:           cfg.Headers,
		LogoutURL:         cfg.LogoutURL,
		HideLocalLogin:    cfg.HideLocalLogin,
	})
}

// SettingsForwardAuthUpdate validates + persists the row, then hot-
// swaps the runtime middleware config. Validation surfaces specific
// 400s for the two failure modes admins are most likely to hit:
// enabling without a CIDR, or pasting a malformed CIDR.
func (h *Handler) SettingsForwardAuthUpdate(c *gin.Context) {
	if h.appSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return
	}
	var body forwardAuthSettingsDTO
	if !bindJSON(c, &body) {
		return
	}
	cfg := repo.ForwardAuthConfig{
		Enabled:           body.Enabled,
		TrustedProxyCIDRs: body.TrustedProxyCIDRs,
		Headers:           body.Headers,
		LogoutURL:         body.LogoutURL,
		HideLocalLogin:    body.HideLocalLogin,
	}
	if err := h.appSettings.SetForwardAuth(c.Request.Context(), cfg); err != nil {
		switch {
		case errors.Is(err, repo.ErrForwardAuthEnabledWithoutCIDR):
			writeError(c, http.StatusBadRequest, err.Error())
		case errors.Is(err, repo.ErrForwardAuthInvalidCIDR),
			errors.Is(err, repo.ErrForwardAuthInvalidHeader),
			errors.Is(err, repo.ErrForwardAuthInvalidLogoutURL):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "forward_auth settings update", err)
		}
		return
	}

	// Re-read to get the normalised values (trimmed, defaults
	// applied) before publishing to the runtime holder.
	saved, err := h.appSettings.GetForwardAuth(c.Request.Context())
	if err != nil {
		writeServerError(c, "forward_auth settings reload", err)
		return
	}
	if h.fwdAuthHolder != nil {
		runtime, rerr := auth.NewForwardAuthConfig(
			saved.Enabled,
			saved.TrustedProxyCIDRs,
			saved.Headers.User,
			saved.Headers.Email,
			saved.Headers.Name,
			saved.Headers.Groups,
			saved.LogoutURL,
			saved.HideLocalLogin,
		)
		if rerr != nil {
			writeServerError(c, "forward_auth runtime rebuild", rerr)
			return
		}
		h.fwdAuthHolder.Set(runtime)
	}
	c.JSON(http.StatusOK, forwardAuthSettingsDTO{
		Enabled:           saved.Enabled,
		TrustedProxyCIDRs: saved.TrustedProxyCIDRs,
		Headers:           saved.Headers,
		LogoutURL:         saved.LogoutURL,
		HideLocalLogin:    saved.HideLocalLogin,
	})
}
