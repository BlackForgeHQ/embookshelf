// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// converterSettingsDTO is the CONVERTER row on the wire. No secrets, so
// no tri-state — the whole row travels both ways.
type converterSettingsDTO struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
}

func (h *Handler) SettingsConverterGet(c *gin.Context) {
	cfg, err := h.appSettings.GetConverter(c.Request.Context())
	if err != nil {
		writeServerError(c, "read converter settings", err)
		return
	}
	c.JSON(http.StatusOK, converterSettingsDTO{Enabled: cfg.Enabled, BaseURL: cfg.BaseURL})
}

func (h *Handler) SettingsConverterUpdate(c *gin.Context) {
	var body converterSettingsDTO
	if !bindJSON(c, &body) {
		return
	}
	next := repo.ConverterConfig{Enabled: body.Enabled, BaseURL: body.BaseURL}
	if err := h.appSettings.SetConverter(c.Request.Context(), next); err != nil {
		// Validation refuses enabling without a URL; that is the admin's
		// mistake to see, not a 500.
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	h.SettingsConverterGet(c)
}

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
	// Candidates is what a run would enqueue right now: unconverted +
	// failed. Derived server-side so the card never re-implements the
	// candidate rule.
	Candidates int `json:"candidates"`
}

// SettingsConverterCoverage answers the bulk card's counts.
func (h *Handler) SettingsConverterCoverage(c *gin.Context) {
	if h.conversionRunner == nil {
		writeError(c, http.StatusServiceUnavailable, "no conversion runner configured")
		return
	}
	cov, err := h.conversionRunner.Coverage(c.Request.Context())
	if err != nil {
		writeServerError(c, "conversion coverage", err)
		return
	}
	c.JSON(http.StatusOK, converterCoverageDTO{
		Total: cov.Total, Ready: cov.Ready, Converting: cov.Converting,
		Failed: cov.Failed, Unconverted: cov.Unconverted,
		Candidates: cov.Unconverted + cov.Failed,
	})
}

// SettingsConverterRun starts a bulk conversion of every candidate.
func (h *Handler) SettingsConverterRun(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := h.appSettings.GetConverter(ctx)
	if err != nil {
		writeServerError(c, "read converter settings", err)
		return
	}
	if !cfg.Enabled || cfg.BaseURL == "" {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeConverterDisabled,
			"converter extension is not configured")
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
	if !cfg.Enabled || cfg.BaseURL == "" {
		c.JSON(http.StatusOK, converterHealthDTO{Status: "not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), converterProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/healthz", nil)
	if err != nil {
		c.JSON(http.StatusOK, converterHealthDTO{Status: "unreachable", Error: err.Error()})
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusOK, converterHealthDTO{Status: "unreachable", Error: err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusOK, converterHealthDTO{
			Status: "unreachable",
			Error:  "healthz answered " + resp.Status,
		})
		return
	}
	c.JSON(http.StatusOK, converterHealthDTO{
		Status:  "ok",
		Version: resp.Header.Get("X-Converter-Version"),
	})
}
