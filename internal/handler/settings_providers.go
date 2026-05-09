// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/service"
)

// SettingsProvidersList returns every known metadata provider with its
// live row (enabled/config/priority) and declared schema. Mirrors the
// subset inside InstanceInfo so the Settings panel can render a full
// form without a second round-trip.
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
		out = append(out, toProviderInfoDTO(p))
	}
	c.JSON(http.StatusOK, gin.H{"providers": out})
}

// settingsProviderPatchReq accepts any combination of the three axes;
// callers include only the fields they're changing. `priorityClear`
// distinguishes "leave as-is" from "reset to nil" (the fallback slot)
// without needing double-pointer unmarshal.
type settingsProviderPatchReq struct {
	Enabled       *bool           `json:"enabled,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
	Priority      *int            `json:"priority,omitempty"`
	PriorityClear bool            `json:"priorityClear,omitempty"`
}

// SettingsProviderUpdate patches one or more axes of a provider row.
// At least one of enabled, config, priority, or priorityClear must be
// present.
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
	if req.Enabled == nil && req.Config == nil && req.Priority == nil && !req.PriorityClear {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nothing to update"})
		return
	}

	ctx := c.Request.Context()
	if req.Config != nil {
		if err := h.enrich.SetProviderConfig(ctx, id, req.Config); err != nil {
			if errors.Is(err, service.ErrUnknownProvider) {
				c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider"})
				return
			}
			slog.Error("set provider config", "id", id, "err", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if req.PriorityClear {
		if err := h.enrich.SetProviderPriority(ctx, id, nil); err != nil {
			if errors.Is(err, service.ErrUnknownProvider) {
				c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider"})
				return
			}
			slog.Error("clear provider priority", "id", id, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update provider"})
			return
		}
	} else if req.Priority != nil {
		if err := h.enrich.SetProviderPriority(ctx, id, req.Priority); err != nil {
			if errors.Is(err, service.ErrUnknownProvider) {
				c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider"})
				return
			}
			slog.Error("set provider priority", "id", id, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update provider"})
			return
		}
	}
	if req.Enabled != nil {
		if err := h.enrich.SetProviderEnabled(ctx, id, *req.Enabled); err != nil {
			if errors.Is(err, service.ErrUnknownProvider) {
				c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider"})
				return
			}
			slog.Error("set provider enabled", "id", id, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update provider"})
			return
		}
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
		out = append(out, toProviderInfoDTO(p))
	}
	c.JSON(http.StatusOK, gin.H{"providers": out})
}
