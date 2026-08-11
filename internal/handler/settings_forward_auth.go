// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
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

// forwardAuthSettings declares the FORWARD_AUTH surface. Validation
// surfaces specific 400s for the failure modes admins are most likely
// to hit: enabling without a CIDR, or pasting a malformed CIDR. After a
// save the runtime middleware config is hot-swapped from the re-read
// (normalized) row.
var forwardAuthSettings = settingsDomain[repo.ForwardAuthConfig, forwardAuthSettingsDTO]{
	name: "forward_auth settings",
	get: func(ctx context.Context, h *Handler) (repo.ForwardAuthConfig, error) {
		return h.appSettings.GetForwardAuth(ctx)
	},
	save: func(ctx context.Context, h *Handler, cfg repo.ForwardAuthConfig) error {
		return h.appSettings.SetForwardAuth(ctx, cfg)
	},
	toDTO: func(_ *Handler, _ *gin.Context, cfg repo.ForwardAuthConfig) forwardAuthSettingsDTO {
		return forwardAuthSettingsDTO{
			Enabled:           cfg.Enabled,
			TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
			Headers:           cfg.Headers,
			LogoutURL:         cfg.LogoutURL,
			HideLocalLogin:    cfg.HideLocalLogin,
		}
	},
	merge: func(dto forwardAuthSettingsDTO, _ repo.ForwardAuthConfig) repo.ForwardAuthConfig {
		return repo.ForwardAuthConfig{
			Enabled:           dto.Enabled,
			TrustedProxyCIDRs: dto.TrustedProxyCIDRs,
			Headers:           dto.Headers,
			LogoutURL:         dto.LogoutURL,
			HideLocalLogin:    dto.HideLocalLogin,
		}
	},
	badRequest: func(err error) bool {
		return errIsOneOf(err,
			repo.ErrForwardAuthEnabledWithoutCIDR,
			repo.ErrForwardAuthInvalidCIDR,
			repo.ErrForwardAuthInvalidHeader,
			repo.ErrForwardAuthInvalidLogoutURL)
	},
	afterSave: func(h *Handler, c *gin.Context, saved repo.ForwardAuthConfig) bool {
		if h.fwdAuthHolder == nil {
			return true
		}
		runtime, err := service.NewForwardAuthRuntime(saved)
		if err != nil {
			writeServerError(c, "forward_auth runtime rebuild", err)
			return false
		}
		h.fwdAuthHolder.Set(runtime)
		return true
	},
}

func (h *Handler) SettingsForwardAuthGet(c *gin.Context) {
	settingsGet(c, h, forwardAuthSettings)
}

func (h *Handler) SettingsForwardAuthUpdate(c *gin.Context) {
	settingsPut(c, h, forwardAuthSettings)
}
