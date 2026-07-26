// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// metadataSettingsDTO is the wire shape for instance-wide metadata
// switches. Today only carries the auto-enrich toggle; future knobs
// (e.g. per-field default provider chain) drop in here.
type metadataSettingsDTO struct {
	AutoEnrich bool `json:"autoEnrich"`
}

// SettingsMetadataGet returns the current metadata settings snapshot.
// Admin-only; mounted under /settings.
func (h *Handler) SettingsMetadataGet(c *gin.Context) {
	ctx := c.Request.Context()
	on, err := h.appSettings.GetBool(ctx, repo.SettingMetadataAutoEnrich)
	if err != nil {
		slog.Error("metadata settings get", "err", err)
		writeError(c, http.StatusInternalServerError, "load metadata settings")
		return
	}
	c.JSON(http.StatusOK, metadataSettingsDTO{AutoEnrich: on})
}

// SettingsMetadataUpdate upserts the metadata settings blob. Accepts
// the same DTO shape as the GET response; missing fields preserve the
// stored value (today only one field, but extending won't break
// existing clients).
func (h *Handler) SettingsMetadataUpdate(c *gin.Context) {
	var body metadataSettingsDTO
	if !bindJSON(c, &body) {
		return
	}
	ctx := c.Request.Context()
	if err := h.appSettings.SetBool(ctx, repo.SettingMetadataAutoEnrich, body.AutoEnrich); err != nil {
		slog.Error("metadata settings update", "err", err)
		writeError(c, http.StatusInternalServerError, "save metadata settings")
		return
	}
	c.JSON(http.StatusOK, body)
}
