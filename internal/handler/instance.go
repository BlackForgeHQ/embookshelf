package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/service"
)

// version returns the build-time app version (set via -ldflags
// -X main.version=...) wired through Deps. Empty falls back to "dev"
// so local `go run` and tests stay readable.
func (h *Handler) appVersion() string {
	if h.version == "" {
		return "dev"
	}
	return h.version
}

type instanceInfoDTO struct {
	Version             string            `json:"version"`
	GoVersion           string            `json:"goVersion"`
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
	// Priority orders provider-chain walks. nil = unranked (fall back
	// to catalog order).
	Priority *int `json:"priority,omitempty"`
	// Config is the stored provider-specific blob. nil when the row
	// hasn't been populated; empty object means "configured, all
	// defaults". Keys are provider-specific — see Schema.
	Config json.RawMessage `json:"config,omitempty"`
	// Schema describes the form fields the settings UI should render.
	// nil when the provider exposes no config knobs.
	Schema []providerConfigFieldDTO `json:"schema,omitempty"`
	// Health telemetry — RFC3339 strings so the client can relativize
	// ("2 minutes ago") without re-parsing Go time formats.
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
	LastErrorAt   string `json:"lastErrorAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

// providerConfigFieldDTO mirrors provider.ConfigField for the wire.
type providerConfigFieldDTO struct {
	Key         string                    `json:"key"`
	Label       string                    `json:"label"`
	Kind        string                    `json:"kind"`
	Placeholder string                    `json:"placeholder,omitempty"`
	Help        string                    `json:"help,omitempty"`
	Options     []providerConfigOptionDTO `json:"options,omitempty"`
}

type providerConfigOptionDTO struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// toProviderInfoDTO flattens a service.ProviderInfo onto the wire
// shape. Keeps the service layer free of json.RawMessage concerns and
// gives the two callers (InstanceInfo + SettingsProvidersList) one
// place to evolve the serialized shape.
func toProviderInfoDTO(p service.ProviderInfo) providerInfoDTO {
	dto := providerInfoDTO{
		ID:        string(p.ID),
		Name:      p.Name,
		Enabled:   p.Enabled,
		External:  p.External,
		Priority:  p.Priority,
		LastError: p.LastError,
	}
	if p.LastSuccessAt != nil {
		dto.LastSuccessAt = p.LastSuccessAt.UTC().Format(time.RFC3339)
	}
	if p.LastErrorAt != nil {
		dto.LastErrorAt = p.LastErrorAt.UTC().Format(time.RFC3339)
	}
	if len(p.Config) > 0 {
		dto.Config = json.RawMessage(p.Config)
	}
	if len(p.Schema) > 0 {
		schema := make([]providerConfigFieldDTO, 0, len(p.Schema))
		for _, f := range p.Schema {
			opts := make([]providerConfigOptionDTO, 0, len(f.Options))
			for _, o := range f.Options {
				opts = append(opts, providerConfigOptionDTO{Value: o.Value, Label: o.Label})
			}
			schema = append(schema, providerConfigFieldDTO{
				Key:         f.Key,
				Label:       f.Label,
				Kind:        string(f.Kind),
				Placeholder: f.Placeholder,
				Help:        f.Help,
				Options:     opts,
			})
		}
		dto.Schema = schema
	}
	return dto
}

// instanceSummaryDTO is the subset of instance facts safe to share with
// every signed-in user — rendered in the persistent status bar.
type instanceSummaryDTO struct {
	Version   string `json:"version"`
	Libraries int    `json:"libraries"`
	Books     int    `json:"books"`
}

// appConfigDTO is the lightweight feature-flag response for GET /api/v1/config.
// All signed-in users can read it so the UI can gate features pre-emptively
// without requiring an admin round-trip.
type appConfigDTO struct {
	// S3Available reports whether EMBOOKSHELF_S3_BUCKET is configured. When
	// false the UI disables the "S3" kind option in the library-create form
	// so the user learns immediately rather than on submit.
	S3Available bool `json:"s3Available"`
	// EmailEnabled mirrors EMAIL.enabled. The login page hides "Forgot
	// password" and the book detail disables Send-to-Kindle when false.
	// ADR-0020.
	EmailEnabled bool `json:"emailEnabled"`
}

// AppConfig returns lightweight feature flags derived from the server
// configuration. Session-authed, not admin-gated.
func (h *Handler) AppConfig(c *gin.Context) {
	c.JSON(http.StatusOK, appConfigDTO{
		S3Available:  h.cfg.SharedS3.Configured(),
		EmailEnabled: h.emailEnabled(),
	})
}

// InstanceSummary returns the non-sensitive instance fields (version
// and catalog-size counts). Session-authed but not admin-gated — the
// status bar at the bottom of every page reads this.
func (h *Handler) InstanceSummary(c *gin.Context) {
	out := instanceSummaryDTO{
		Version: h.appVersion(),
	}
	// Counts are best-effort: a DB hiccup here shouldn't take down the
	// status bar, so we log and render zeros rather than erroring.
	if libs, err := h.lib.List(c.Request.Context()); err == nil {
		out.Libraries = len(libs)
		for _, l := range libs {
			out.Books += l.BookCount
		}
	} else {
		slog.Warn("instance summary counts", "err", err)
	}
	c.JSON(http.StatusOK, out)
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
		providers = append(providers, toProviderInfoDTO(p))
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
		Version:             h.appVersion(),
		GoVersion:           runtime.Version(),
		AllowedOrigins:      h.cfg.AllowedOrigins,
		BookDropPath:        h.cfg.BookDropPath,
		DataPath:            h.cfg.DataPath,
		MigrateOnStart:      h.cfg.MigrateOnStart,
		EnrichmentProviders: providers,
		Counts:              counts,
	})
}
