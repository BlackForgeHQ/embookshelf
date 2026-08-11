// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// requireConverter resolves the converter gate for a route whose work
// needs the extension, mirroring requireQueue: one place owns the 503 +
// CodeConverterDisabled + refusal string, because restating the
// predicate per call site is how exactly this drift once
// nil-dereferenced Send-to-Kindle on the queue seam (#223, #298).
//
// A caller gets the config it can dial or false and a response already
// written. Not for SettingsConverterHealth, whose not-configured answer
// is a 200 status card, not a refusal.
func (h *Handler) requireConverter(c *gin.Context) (repo.ConverterConfig, bool) {
	cfg, err := h.appSettings.GetConverter(c.Request.Context())
	if err != nil {
		writeServerError(c, "read converter settings", err)
		return repo.ConverterConfig{}, false
	}
	if !cfg.Configured() {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeConverterDisabled,
			repo.MsgConverterNotConfigured)
		return repo.ConverterConfig{}, false
	}
	return cfg, true
}

// converterSettingsDTO is the CONVERTER row on the wire. No secrets, so
// no tri-state — the whole row travels both ways.
type converterSettingsDTO struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
}

// converterSettings declares the CONVERTER surface. No secrets, no
// side effects; every save refusal maps to a 400 — validation refuses
// enabling without a URL, and that is the admin's mistake to see, not
// a 500.
var converterSettings = settingsDomain[repo.ConverterConfig, converterSettingsDTO]{
	name: "converter settings",
	get: func(ctx context.Context, h *Handler) (repo.ConverterConfig, error) {
		return h.appSettings.GetConverter(ctx)
	},
	save: func(ctx context.Context, h *Handler, cfg repo.ConverterConfig) error {
		return h.appSettings.SetConverter(ctx, cfg)
	},
	toDTO: func(_ *Handler, _ *gin.Context, cfg repo.ConverterConfig) converterSettingsDTO {
		return converterSettingsDTO{Enabled: cfg.Enabled, BaseURL: cfg.BaseURL}
	},
	merge: func(dto converterSettingsDTO, _ repo.ConverterConfig) repo.ConverterConfig {
		return repo.ConverterConfig{Enabled: dto.Enabled, BaseURL: dto.BaseURL}
	},
	badRequest: anySaveRefusalIsA400,
}

func (h *Handler) SettingsConverterGet(c *gin.Context) { settingsGet(c, h, converterSettings) }

func (h *Handler) SettingsConverterUpdate(c *gin.Context) { settingsPut(c, h, converterSettings) }

// converterCoverageDTO is the bulk-conversion card's numbers: the
// pre-flight ("candidates would convert") and the progress poll are the
// same answer, so a bar built from it survives reload and restart —
// the same property the guide run's coverage has.
type converterCoverageDTO struct {
	Total       int `json:"total"`
	Ready       int `json:"ready"`
	Converting  int `json:"converting"`
	Failed      int `json:"failed"`
	Unconverted int `json:"unconverted"`
	// Candidates is what a run would enqueue right now, answered by the
	// repo's own Candidates() — the counting side of the candidate
	// query, so the card never re-implements the rule.
	Candidates int `json:"candidates"`
}

// SettingsConverterCoverage answers the bulk card's counts.
func (h *Handler) SettingsConverterCoverage(c *gin.Context) {
	if h.renditions == nil {
		writeError(c, http.StatusServiceUnavailable, "markdown renditions are unavailable")
		return
	}
	cov, err := h.renditions.CountConversionCoverage(c.Request.Context())
	if err != nil {
		writeServerError(c, "conversion coverage", err)
		return
	}
	c.JSON(http.StatusOK, converterCoverageDTO{
		Total: cov.Total, Ready: cov.Ready, Converting: cov.Converting,
		Failed: cov.Failed, Unconverted: cov.Unconverted,
		Candidates: cov.Candidates(),
	})
}

// SettingsConverterRun starts a bulk conversion of every candidate.
func (h *Handler) SettingsConverterRun(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := h.requireConverter(c); !ok {
		return
	}
	if h.conversionRunner == nil {
		writeError(c, http.StatusServiceUnavailable, "no conversion runner configured")
		return
	}
	queued, err := h.conversionRunner.Start(ctx)
	if err != nil {
		// Partial progress is real: those jobs are running. Report the
		// count alongside the failure rather than implying nothing began.
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "bulk conversion partially queued: " + err.Error(),
			"queued": queued,
		})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": queued})
}

// converterHealthDTO is the admin card's status answer. Status is
// "not_configured" (disabled or no URL — no probe attempted),
// "ok" (the sidecar answered /healthz), or "unreachable" (it did not;
// Error carries why, verbatim, per ADR-0033's loud-failure rule).
type converterHealthDTO struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// converterProbeTimeout bounds the health probe: the card polls, and an
// admin staring at a spinner learns less than one staring at
// "unreachable: context deadline exceeded".
const converterProbeTimeout = 3 * time.Second

func (h *Handler) SettingsConverterHealth(c *gin.Context) {
	cfg, err := h.appSettings.GetConverter(c.Request.Context())
	if err != nil {
		writeServerError(c, "read converter settings", err)
		return
	}
	if !cfg.Configured() {
		c.JSON(http.StatusOK, converterHealthDTO{Status: "not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), converterProbeTimeout)
	defer cancel()
	version, err := (&service.ConverterClient{}).Health(ctx, cfg.BaseURL)
	if err != nil {
		c.JSON(http.StatusOK, converterHealthDTO{Status: "unreachable", Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, converterHealthDTO{Status: "ok", Version: version})
}
