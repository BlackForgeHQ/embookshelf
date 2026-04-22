package handler

import (
	"log/slog"
	"net/http"
	"runtime"

	"github.com/gin-gonic/gin"
)

// Version is the build-time app version. Bumped by hand; the status bar
// and About panel both read this single source.
const Version = "1.0.0"

type instanceInfoDTO struct {
	Version             string            `json:"version"`
	GoVersion           string            `json:"goVersion"`
	DiskMode            string            `json:"diskMode"`
	AllowedOrigins      []string          `json:"allowedOrigins"`
	BookDropPath        string            `json:"bookDropPath"`
	DataPath            string            `json:"dataPath"`
	MigrateOnStart      bool              `json:"migrateOnStart"`
	EnrichmentProviders []providerInfoDTO `json:"enrichmentProviders"`
	Counts              instanceCountsDTO `json:"counts"`
}

type instanceCountsDTO struct {
	Users     int `json:"users"`
	Libraries int `json:"libraries"`
	Books     int `json:"books"`
}

type providerInfoDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	External bool   `json:"external"`
}

// instanceSummaryDTO is the subset of instance facts safe to share with
// every signed-in user — rendered in the persistent status bar.
type instanceSummaryDTO struct {
	Version  string `json:"version"`
	DiskMode string `json:"diskMode"`
}

// InstanceSummary returns the non-sensitive instance fields (version,
// disk mode). Session-authed but not admin-gated — the status bar at the
// bottom of every page reads this.
func (h *Handler) InstanceSummary(c *gin.Context) {
	c.JSON(http.StatusOK, instanceSummaryDTO{
		Version:  Version,
		DiskMode: h.cfg.DiskType,
	})
}

// InstanceInfo returns the server-side facts the settings panels need:
// version, mode, configured providers, aggregate counts. Admin-only —
// mounted under the admin group.
func (h *Handler) InstanceInfo(c *gin.Context) {
	ctx := c.Request.Context()

	infos, err := h.enrich.ListProviders(ctx)
	if err != nil {
		slog.Warn("list providers", "err", err)
	}
	providers := make([]providerInfoDTO, 0, len(infos))
	for _, p := range infos {
		providers = append(providers, providerInfoDTO{
			ID:       string(p.ID),
			Name:     p.Name,
			Enabled:  p.Enabled,
			External: p.External,
		})
	}

	counts := instanceCountsDTO{}
	if libs, err := h.lib.List(ctx); err == nil {
		counts.Libraries = len(libs)
		for _, l := range libs {
			counts.Books += l.BookCount
		}
	}
	if users, err := h.auth.ListUsers(ctx); err == nil {
		counts.Users = len(users)
	}

	c.JSON(http.StatusOK, instanceInfoDTO{
		Version:             Version,
		GoVersion:           runtime.Version(),
		DiskMode:            h.cfg.DiskType,
		AllowedOrigins:      h.cfg.AllowedOrigins,
		BookDropPath:        h.cfg.BookDropPath,
		DataPath:            h.cfg.DataPath,
		MigrateOnStart:      h.cfg.MigrateOnStart,
		EnrichmentProviders: providers,
		Counts:              counts,
	})
}
