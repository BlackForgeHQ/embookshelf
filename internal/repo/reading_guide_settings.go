// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"strings"
)

// SettingReadingGuide names the app_settings row holding the reading
// guide generator's configuration (ADR-0024).
const SettingReadingGuide = "READING_GUIDE"

// DefaultReadingGuideTextCap is ~12k tokens of book text: roughly the
// opening 30-40 pages, which is where a book states what it is and who
// it is for. Fits a 16k-context local model and costs cents per book.
// A real 300-page EPUB extracts to ~433k characters, so this binds for
// most books — deliberately, since it is the cost dial.
const DefaultReadingGuideTextCap int64 = 48_000

// ReadingGuideConfig is the READING_GUIDE row.
//
// One OpenAI-compatible endpoint covers cloud and local alike (ADR-0024
// §3): an operator who does not want their library read by a third party
// points BaseURL at a local Ollama and the book text never leaves the
// machine. APIKey is encrypted at rest like every other credential in
// this table (ADR-0010).
type ReadingGuideConfig struct {
	// Enabled gates the feature. Off by default — generation costs money
	// and nothing should spend it before an admin says so.
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey,omitempty"`
	// AuthStyle is "bearer" (default) or "api-key". Azure OpenAI wants
	// the latter; everything else wants the former.
	AuthStyle string `json:"authStyle"`
	// Language every guide is written in, regardless of the book's own.
	Language string `json:"language"`
	// TextCap bounds how much book text reaches the model.
	TextCap int64 `json:"textCap"`
	// RequestJSONMode sends response_format: json_object. Off by default
	// because support is uneven across OpenAI-compatible servers; the
	// prompt carries the JSON contract regardless.
	RequestJSONMode bool `json:"requestJsonMode"`
}

func DefaultReadingGuideConfig() ReadingGuideConfig {
	return ReadingGuideConfig{
		AuthStyle: "bearer",
		Language:  "en",
		TextCap:   DefaultReadingGuideTextCap,
	}
}

var readingGuideSetting = Setting[ReadingGuideConfig]{
	Key:     SettingReadingGuide,
	Default: DefaultReadingGuideConfig,
	Normalize: func(cfg ReadingGuideConfig) ReadingGuideConfig {
		// Operators paste endpoints with a trailing slash; "/v1/" plus
		// "/chat/completions" would produce a double slash.
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		cfg.Model = strings.TrimSpace(cfg.Model)
		cfg.APIKey = strings.TrimSpace(cfg.APIKey)
		cfg.AuthStyle = strings.ToLower(strings.TrimSpace(cfg.AuthStyle))
		if cfg.AuthStyle != "api-key" {
			cfg.AuthStyle = "bearer"
		}
		cfg.Language = strings.ToLower(strings.TrimSpace(cfg.Language))
		if cfg.Language == "" {
			cfg.Language = "en"
		}
		// A zero or negative cap would mean "send the entire book", which
		// is the one value this field must never take by accident.
		if cfg.TextCap <= 0 {
			cfg.TextCap = DefaultReadingGuideTextCap
		}
		return cfg
	},
	Validate: func(cfg ReadingGuideConfig) error {
		// Only completeness-on-enable: an admin fills the form in stages,
		// and refusing a half-filled disabled row would make the panel
		// unusable. Enabling without an endpoint, though, ships a feature
		// that fails on every click.
		if !cfg.Enabled {
			return nil
		}
		if cfg.BaseURL == "" {
			return errors.New("reading guides need a base URL before they can be enabled")
		}
		if cfg.Model == "" {
			return errors.New("reading guides need a model before they can be enabled")
		}
		return nil
	},
	Secrets: func(cfg *ReadingGuideConfig) []*string { return []*string{&cfg.APIKey} },
}

// GetReadingGuide loads the row with the API key decrypted. A missing row
// yields defaults and a nil error, so first boot needs no seed.
func (r *AppSettingsRepo) GetReadingGuide(ctx context.Context) (ReadingGuideConfig, error) {
	return readingGuideSetting.Get(ctx, r)
}

// SetReadingGuide normalizes, validates, encrypts the key and upserts.
func (r *AppSettingsRepo) SetReadingGuide(ctx context.Context, cfg ReadingGuideConfig) error {
	return readingGuideSetting.Set(ctx, r, cfg)
}

// SeedReadingGuideIfAbsent writes a disabled default row so the settings
// panel has something to edit on first boot.
func (r *AppSettingsRepo) SeedReadingGuideIfAbsent(ctx context.Context) error {
	return readingGuideSetting.SeedIfAbsent(ctx, r)
}
