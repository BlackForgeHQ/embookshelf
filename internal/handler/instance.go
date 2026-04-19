package handler

import (
	"net/http"
	"runtime"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/provider"
)

// Version is the build-time app version. Bumped by hand; the status bar
// and About panel both read this single source.
const Version = "1.4.2"

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

// providerCatalog enumerates every source the binary can build. The Enabled
// flag is filled in per-request from config; the labels/URLs are static.
var providerCatalog = []struct {
	ID, Name string
}{
	{ID: string(provider.SourceGoogleBooks), Name: "Google Books"},
	{ID: string(provider.SourceOpenLibrary), Name: "Open Library"},
	{ID: string(provider.SourceAmazon), Name: "Amazon"},
	{ID: string(provider.SourceDuckDuckGo), Name: "DuckDuckGo"},
}

// InstanceInfo returns the server-side facts the settings panels need:
// version, mode, configured providers, aggregate counts. Admin-only —
// mounted under the admin group.
func (h *Handler) InstanceInfo(c *gin.Context) {
	ctx := c.Request.Context()

	enabled := make(map[string]struct{}, len(h.cfg.EnrichmentProviders))
	for _, p := range h.cfg.EnrichmentProviders {
		enabled[p] = struct{}{}
	}
	providers := make([]providerInfoDTO, 0, len(providerCatalog))
	for _, p := range providerCatalog {
		_, on := enabled[p.ID]
		providers = append(providers, providerInfoDTO{
			ID:       p.ID,
			Name:     p.Name,
			Enabled:  on,
			External: true,
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
