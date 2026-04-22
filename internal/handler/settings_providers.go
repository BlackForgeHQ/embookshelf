package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/service"
)

// SettingsProvidersList returns every known metadata provider plus its
// current enabled flag. Mirrors the shape inside InstanceInfo so the
// Settings panel can also toggle individual rows (PATCH below) without
// refetching the whole instance blob.
func (h *Handler) SettingsProvidersList(c *gin.Context) {
	ctx := c.Request.Context()

	infos, err := h.enrich.ListProviders(ctx)
	if err != nil {
		slog.Error("list providers", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list providers"})
		return
	}

	out := make([]providerInfoDTO, 0, len(infos))
	for _, p := range infos {
		out = append(out, providerInfoDTO{
			ID:       string(p.ID),
			Name:     p.Name,
			Enabled:  p.Enabled,
			External: p.External,
		})
	}
	c.JSON(http.StatusOK, gin.H{"providers": out})
}

type settingsProviderPatchReq struct {
	Enabled *bool `json:"enabled"`
}

// SettingsProviderUpdate toggles the enabled flag for one provider. The
// id must match the static catalog; unknown ids get 404. `enabled` is a
// pointer so a missing field is rejected as "nothing to update" rather
// than silently treated as false.
func (h *Handler) SettingsProviderUpdate(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	var req settingsProviderPatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled required"})
		return
	}

	ctx := c.Request.Context()
	if err := h.enrich.SetProviderEnabled(ctx, id, *req.Enabled); err != nil {
		if errors.Is(err, service.ErrUnknownProvider) {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider"})
			return
		}
		slog.Error("set provider enabled", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update provider"})
		return
	}

	// Return the full refreshed list so the client can reconcile its
	// cache without a second round-trip.
	infos, err := h.enrich.ListProviders(ctx)
	if err != nil {
		slog.Error("list providers after update", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list providers"})
		return
	}
	out := make([]providerInfoDTO, 0, len(infos))
	for _, p := range infos {
		out = append(out, providerInfoDTO{
			ID:       string(p.ID),
			Name:     p.Name,
			Enabled:  p.Enabled,
			External: p.External,
		})
	}
	c.JSON(http.StatusOK, gin.H{"providers": out})
}
