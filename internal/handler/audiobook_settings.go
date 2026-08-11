// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/tts"
)

// audiobookEngineDTO is one engine's row in the settings panel, and the
// same shape a PUT submits back.
//
// KeySet rather than the key itself: a GET must never hand back a
// credential it was given. APIKey is write-only — never populated on
// the way out — and rides with KeySet so an empty submitted key can
// mean either "keep the stored one" or "clear it", the same tri-state
// the reading-guide panel uses. Label and the Needs*/MaxRequestChars
// fields are read-only catalog facts a submission cannot change.
type audiobookEngineDTO struct {
	ID                   string  `json:"id"`
	Label                string  `json:"label"`
	Enabled              bool    `json:"enabled"`
	BaseURL              string  `json:"baseUrl"`
	APIKey               string  `json:"apiKey,omitempty"`
	KeySet               bool    `json:"keySet"`
	Model                string  `json:"model"`
	DefaultVoice         string  `json:"defaultVoice"`
	PricePerMillionChars float64 `json:"pricePerMillionChars"`
	MaxRequestChars      int     `json:"maxRequestChars"`
	NeedsModel           bool    `json:"needsModel"`
	NeedsBaseURL         bool    `json:"needsBaseUrl"`
}

// Deliberately does not expose segmentChars or requestTimeoutSeconds.
// ADR-0028 §3 states the segment cap as a fixed property of the design —
// it is what keeps a job inside River's rescue window — and surfacing it
// as a knob would invite an admin to set a value that breaks that.
type audiobookSettingsDTO struct {
	Enabled bool                 `json:"enabled"`
	Engine  string               `json:"engine"`
	Engines []audiobookEngineDTO `json:"engines"`
}

// audiobookSettings declares the AUDIOBOOK surface. The engine list is
// the one dynamic secret set among the settings domains: each submitted
// engine that matches a config slot carries its own tri-state API key.
// Every save refusal maps to a 400 — same reasoning as the reading
// guide surface.
var audiobookSettings = settingsDomain[repo.AudiobookConfig, audiobookSettingsDTO]{
	name: "audiobook settings",
	get: func(ctx context.Context, h *Handler) (repo.AudiobookConfig, error) {
		return h.appSettings.GetAudiobook(ctx)
	},
	save: func(ctx context.Context, h *Handler, cfg repo.AudiobookConfig) error {
		return h.appSettings.SetAudiobook(ctx, cfg)
	},
	toDTO: func(_ *Handler, _ *gin.Context, cfg repo.AudiobookConfig) audiobookSettingsDTO {
		return audiobookSettingsToDTO(cfg)
	},
	merge: func(req audiobookSettingsDTO, current repo.AudiobookConfig) repo.AudiobookConfig {
		next := current
		next.Enabled = req.Enabled
		next.Engine = req.Engine
		for _, in := range req.Engines {
			slot := next.EngineSlot(tts.EngineID(in.ID))
			if slot == nil {
				continue
			}
			slot.Enabled = in.Enabled
			slot.BaseURL = in.BaseURL
			slot.Model = in.Model
			slot.DefaultVoice = in.DefaultVoice
			slot.PricePerMillionChars = in.PricePerMillionChars
		}
		return next
	},
	secrets: func(req *audiobookSettingsDTO, next, current *repo.AudiobookConfig) []settingsSecret {
		out := make([]settingsSecret, 0, len(req.Engines))
		for _, in := range req.Engines {
			slot := next.EngineSlot(tts.EngineID(in.ID))
			if slot == nil {
				continue
			}
			out = append(out, settingsSecret{
				incoming: in.APIKey,
				set:      in.KeySet,
				stored:   slot.APIKey,
				slot:     &slot.APIKey,
			})
		}
		return out
	},
	badRequest: anySaveRefusalIsA400,
}

func (h *Handler) SettingsAudiobookGet(c *gin.Context) { settingsGet(c, h, audiobookSettings) }

func (h *Handler) SettingsAudiobookUpdate(c *gin.Context) { settingsPut(c, h, audiobookSettings) }

// SettingsAudiobookVoices proxies the selected engine's voice list, which
// is what populates the generate dialog's picker.
//
// Uncached: voice lists change when someone clones a voice, and the call
// happens once per dialog open rather than once per page.
func (h *Handler) SettingsAudiobookVoices(c *gin.Context) {
	sel, ok := h.probeEngine(c)
	if !ok {
		return
	}
	voices, err := sel.Engine.ListVoices(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]gin.H, 0, len(voices))
	for _, v := range voices {
		out = append(out, gin.H{"id": v.ID, "label": v.Label})
	}
	c.JSON(http.StatusOK, gin.H{"voices": out})
}

// SettingsAudiobookTest synthesizes one short phrase so an admin finds
// out the key is wrong now rather than forty minutes into a run.
func (h *Handler) SettingsAudiobookTest(c *gin.Context) {
	sel, ok := h.probeEngine(c)
	if !ok {
		return
	}
	audio, err := sel.Engine.Synthesize(c.Request.Context(), tts.Request{
		Text:  "This is a test of audiobook narration.",
		Voice: sel.Settings.DefaultVoice,
		Model: sel.Settings.Model,
	})
	if err != nil {
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "engine": string(sel.ID), "bytes": len(audio)})
}

// probeEngine builds the selected engine for an admin-facing probe,
// writing the error response itself when it cannot.
//
// The row owns the construction, so this surface and the queue worker
// cannot disagree about which stored fields reach the adapter.
func (h *Handler) probeEngine(c *gin.Context) (repo.ConfiguredEngine, bool) {
	var zero repo.ConfiguredEngine
	if h.appSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return zero, false
	}
	cfg, err := h.appSettings.GetAudiobook(c.Request.Context())
	if err != nil {
		writeServerError(c, "audiobook settings", err)
		return zero, false
	}
	sel, err := cfg.ProbeEngine()
	if err != nil {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeAudiobooksDisabled, err.Error())
		return zero, false
	}
	return sel, true
}

// audiobookSettingsToDTO walks the catalog rather than the config, so the
// panel renders every engine the binary knows even before any of them has
// been configured.
func audiobookSettingsToDTO(cfg repo.AudiobookConfig) audiobookSettingsDTO {
	engines := make([]audiobookEngineDTO, 0, len(tts.Catalog))
	for _, info := range tts.Catalog {
		slot := cfg.EngineSlot(info.ID)
		if slot == nil {
			continue
		}
		engines = append(engines, audiobookEngineDTO{
			ID:                   string(info.ID),
			Label:                info.Label,
			Enabled:              slot.Enabled,
			BaseURL:              slot.BaseURL,
			KeySet:               slot.APIKey != "",
			Model:                slot.Model,
			DefaultVoice:         slot.DefaultVoice,
			PricePerMillionChars: slot.PricePerMillionChars,
			MaxRequestChars:      info.MaxRequestChars,
			NeedsModel:           info.NeedsModel,
			NeedsBaseURL:         info.DefaultBaseURL == "",
		})
	}
	return audiobookSettingsDTO{
		Enabled: cfg.Enabled,
		Engine:  cfg.Engine,
		Engines: engines,
	}
}
