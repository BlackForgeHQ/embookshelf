// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/tts"
)

// SettingAudiobook names the app_settings row holding audiobook
// generation's configuration (ADR-0026 §5).
const SettingAudiobook = "AUDIOBOOK"

// DefaultAudiobookSegmentChars is one unit of synthesis: roughly 45
// minutes of audio, a few tens of cents to retry, and comfortably inside
// River's ~1h rescue window.
//
// Aliased from fileproc rather than restated, so the settings default and
// the splitter that enforces it cannot drift apart.
//
// Not the same as an engine's per-request cap, which is far smaller — a
// segment is one job and one retry, and the adapter splits it into as
// many engine calls as that engine's limit requires.
const DefaultAudiobookSegmentChars = fileproc.DefaultSegmentChars

// DefaultAudiobookRequestTimeoutSeconds bounds one engine call.
const DefaultAudiobookRequestTimeoutSeconds = 300

// AudiobookEngineConfig is one engine's slice of the settings row.
//
// Uniform across all three engines, which is exactly why this lives in a
// typed app_settings row rather than in provider_settings: that table's
// runtime ConfigSchema machinery exists for providers whose config
// genuinely diverges, and buying it here would cost the safer encryption
// path for nothing (ADR-0026 §5).
type AudiobookEngineConfig struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey,omitempty"`
	Model   string `json:"model"`
	// DefaultVoice prefills the generate dialog. Per-book overrides are
	// the point of that dialog, so this is a starting point, not a policy.
	DefaultVoice string `json:"defaultVoice"`
	// PricePerMillionChars drives the pre-flight estimate. Admin-owned and
	// catalog-prefilled: the number shown is real money, prices change
	// without our releases, and a stale default is the operator's to fix
	// rather than a bug we cannot close (ADR-0028 §2).
	PricePerMillionChars float64 `json:"pricePerMillionChars"`
}

// AudiobookConfig is the AUDIOBOOK row.
type AudiobookConfig struct {
	// Enabled gates the feature. Off by default — a run costs real money
	// and nothing should spend it before an admin says so.
	Enabled bool `json:"enabled"`
	// Engine is the *selected* engine, not a ranking. Narration through
	// three engines would produce three books and three bills, so the
	// fan-out vocabulary of metadata providers does not apply here.
	Engine                string `json:"engine"`
	SegmentChars          int    `json:"segmentChars"`
	RequestTimeoutSeconds int    `json:"requestTimeoutSeconds"`

	OpenAI     AudiobookEngineConfig `json:"openai"`
	ElevenLabs AudiobookEngineConfig `json:"elevenlabs"`
	Azure      AudiobookEngineConfig `json:"azure"`
}

// EngineSlot returns a pointer to the sub-struct for id, so no caller
// repeats the switch. Nil for an unknown id.
//
// Exported because the settings handler needs it too: it was briefly
// duplicated there, which is one dispatch on EngineID in two places that
// nothing forces to agree — exactly what the OIDC provider registry
// exists to avoid.
func (c *AudiobookConfig) EngineSlot(id tts.EngineID) *AudiobookEngineConfig {
	switch id {
	case tts.EngineOpenAI:
		return &c.OpenAI
	case tts.EngineElevenLabs:
		return &c.ElevenLabs
	case tts.EngineAzure:
		return &c.Azure
	}
	return nil
}

// SelectedEngine resolves the configured engine to its catalog id and its
// settings. Named once because every generate, estimate and connection
// test asks the same question.
func (c AudiobookConfig) SelectedEngine() (tts.EngineID, AudiobookEngineConfig, error) {
	id := tts.EngineID(c.Engine)
	slot := (&c).EngineSlot(id)
	if slot == nil {
		return "", AudiobookEngineConfig{}, fmt.Errorf("audiobook: no engine %q in the catalog", c.Engine)
	}
	return id, *slot, nil
}

// DefaultAudiobookConfig is the seeded row: disabled, with each engine
// prefilled from the catalog so the settings panel and the estimate have
// something sensible to show before anyone types.
func DefaultAudiobookConfig() AudiobookConfig {
	cfg := AudiobookConfig{
		Engine:                string(tts.EngineOpenAI),
		SegmentChars:          DefaultAudiobookSegmentChars,
		RequestTimeoutSeconds: DefaultAudiobookRequestTimeoutSeconds,
	}
	for _, info := range tts.Catalog {
		if slot := (&cfg).EngineSlot(info.ID); slot != nil {
			slot.BaseURL = info.DefaultBaseURL
			slot.Model = info.DefaultModel
			slot.PricePerMillionChars = info.DefaultPricePerMillionChars
		}
	}
	return cfg
}

var audiobookSetting = Setting[AudiobookConfig]{
	Key:     SettingAudiobook,
	Default: DefaultAudiobookConfig,
	Normalize: func(cfg AudiobookConfig) AudiobookConfig {
		cfg.Engine = strings.ToLower(strings.TrimSpace(cfg.Engine))
		if cfg.SegmentChars <= 0 {
			cfg.SegmentChars = DefaultAudiobookSegmentChars
		}
		if cfg.RequestTimeoutSeconds <= 0 {
			cfg.RequestTimeoutSeconds = DefaultAudiobookRequestTimeoutSeconds
		}
		for _, info := range tts.Catalog {
			slot := (&cfg).EngineSlot(info.ID)
			if slot == nil {
				continue
			}
			slot.BaseURL = strings.TrimRight(strings.TrimSpace(slot.BaseURL), "/")
			slot.APIKey = strings.TrimSpace(slot.APIKey)
			slot.Model = strings.TrimSpace(slot.Model)
			slot.DefaultVoice = strings.TrimSpace(slot.DefaultVoice)
			// Zero is a real price — a local engine costs nothing, and
			// overwriting that with the catalog default would quote $8 for
			// a run that is free. Only a negative price is meaningless.
			if slot.PricePerMillionChars < 0 {
				slot.PricePerMillionChars = info.DefaultPricePerMillionChars
			}
		}
		return cfg
	},
	Validate: func(cfg AudiobookConfig) error {
		// Completeness only on enable: an admin fills the form in stages,
		// and refusing a half-filled disabled row makes the panel unusable.
		if !cfg.Enabled {
			return nil
		}
		id, engine, err := cfg.SelectedEngine()
		if err != nil {
			return errors.New("pick an engine before enabling audiobook generation")
		}
		if !engine.Enabled {
			return fmt.Errorf("%s is selected but not enabled", id)
		}
		info, _ := tts.Lookup(id)
		if engine.BaseURL == "" && info.DefaultBaseURL == "" {
			return fmt.Errorf("%s needs a base URL", id)
		}
		if engine.DefaultVoice == "" {
			return fmt.Errorf("%s needs a default voice", id)
		}
		if info.NeedsModel && engine.Model == "" {
			return fmt.Errorf("%s needs a model", id)
		}
		// OpenAI-compatible is the one engine that legitimately runs
		// without a key: a local Kokoro or openedai-speech has none, and
		// that is the configuration self-hosters most want.
		if engine.APIKey == "" && id != tts.EngineOpenAI {
			return fmt.Errorf("%s needs an API key", id)
		}
		return nil
	},
	Secrets: func(cfg *AudiobookConfig) []*string {
		return []*string{&cfg.OpenAI.APIKey, &cfg.ElevenLabs.APIKey, &cfg.Azure.APIKey}
	},
}

// GetAudiobook loads the row with every API key decrypted.
func (r *AppSettingsRepo) GetAudiobook(ctx context.Context) (AudiobookConfig, error) {
	return audiobookSetting.Get(ctx, r)
}

// SetAudiobook normalizes, validates, encrypts the keys and upserts.
func (r *AppSettingsRepo) SetAudiobook(ctx context.Context, cfg AudiobookConfig) error {
	return audiobookSetting.Set(ctx, r, cfg)
}

// SeedAudiobookIfAbsent writes a disabled default row so the settings
// panel has something to edit on first boot.
func (r *AppSettingsRepo) SeedAudiobookIfAbsent(ctx context.Context) error {
	return audiobookSetting.SeedIfAbsent(ctx, r)
}
