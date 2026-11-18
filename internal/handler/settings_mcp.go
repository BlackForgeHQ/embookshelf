// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// mcpSettingsDTO is the admin-edit shape for the MCP row (ADR-0039 §4).
type mcpSettingsDTO struct {
	Enabled bool `json:"enabled"`
}

// mcpSettings declares the MCP surface. The save's side effect publishes
// the flag to the runtime switch, so turning MCP off cuts every client
// off on the next request, without a restart.
var mcpSettings = settingsDomain[repo.MCPConfig, mcpSettingsDTO]{
	name: "mcp settings",
	get: func(ctx context.Context, h *Handler) (repo.MCPConfig, error) {
		return h.appSettings.GetMCP(ctx)
	},
	save: func(ctx context.Context, h *Handler, cfg repo.MCPConfig) error {
		return h.appSettings.SetMCP(ctx, cfg)
	},
	toDTO: func(_ *Handler, _ *gin.Context, cfg repo.MCPConfig) mcpSettingsDTO {
		return mcpSettingsDTO{Enabled: cfg.Enabled}
	},
	merge: func(dto mcpSettingsDTO, _ repo.MCPConfig) repo.MCPConfig {
		return repo.MCPConfig{Enabled: dto.Enabled}
	},
	afterSave: func(h *Handler, _ *gin.Context, saved repo.MCPConfig) bool {
		if h.mcp != nil {
			h.mcp.Set(saved.Enabled)
		}
		return true
	},
}

func (h *Handler) SettingsMCPGet(c *gin.Context) {
	settingsGet(c, h, mcpSettings)
}

func (h *Handler) SettingsMCPUpdate(c *gin.Context) {
	settingsPut(c, h, mcpSettings)
}

// SettingsMCPTokensList returns every user's tokens with their owners.
// Answers while MCP is off too — the admin may be cleaning up.
func (h *Handler) SettingsMCPTokensList(c *gin.Context) {
	rows, err := h.apiTokens.ListAll(c.Request.Context())
	if err != nil {
		writeServerError(c, "mcp tokens list", err)
		return
	}
	out := make([]adminAPITokenDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, adminAPITokenDTO{
			apiTokenDTO: toAPITokenDTO(r.APIToken),
			Owner: apiTokenOwnerDTO{
				ID:    r.UserID,
				Email: r.OwnerEmail,
				Name:  r.OwnerName,
			},
		})
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

// SettingsMCPTokenRevoke deletes any user's token.
func (h *Handler) SettingsMCPTokenRevoke(c *gin.Context) {
	err := h.apiTokens.RevokeAny(c.Request.Context(), c.Param("id"))
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "token not found")
	default:
		writeServerError(c, "mcp token revoke", err)
	}
}
