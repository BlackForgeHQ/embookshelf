// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/tts"
)

// The row is the single place that turns stored settings into a live
// client. These tests pin every field the row declares against the config
// handed to the adapter — the drift that let the reading guide worker
// silently drop the configured auth style and fall back to bearer.

func TestReadingGuideLLMConfigCarriesEveryStoredField(t *testing.T) {
	cfg := ReadingGuideConfig{
		Enabled:         true,
		BaseURL:         "https://example.invalid/openai/v1",
		Model:           "gpt-4o-mini",
		APIKey:          "secret",
		AuthStyle:       "api-key",
		RequestJSONMode: true,
	}

	got := cfg.llmConfig(llm.DefaultTimeout)

	if got.BaseURL != cfg.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, cfg.BaseURL)
	}
	if got.Model != cfg.Model {
		t.Errorf("Model = %q, want %q", got.Model, cfg.Model)
	}
	if got.APIKey != cfg.APIKey {
		t.Errorf("APIKey = %q, want %q", got.APIKey, cfg.APIKey)
	}
	if got.AuthStyle != llm.AuthAPIKeyHeader {
		t.Errorf("AuthStyle = %q, want %q", got.AuthStyle, llm.AuthAPIKeyHeader)
	}
	if !got.RequestJSONMode {
		t.Error("RequestJSONMode = false, want true")
	}
	if got.Timeout != llm.DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", got.Timeout, llm.DefaultTimeout)
	}
}

func TestReadingGuideProbeUsesShortTimeout(t *testing.T) {
	cfg := ReadingGuideConfig{Enabled: true, BaseURL: "https://example.invalid", Model: "m"}

	if got := cfg.llmConfig(probeTimeout).Timeout; got != probeTimeout {
		t.Errorf("probe timeout = %v, want %v", got, probeTimeout)
	}
	if probeTimeout >= llm.DefaultTimeout {
		t.Errorf("probe timeout %v should be shorter than the generation timeout %v", probeTimeout, llm.DefaultTimeout)
	}
}

func TestAudiobookTTSConfigCarriesRequestTimeout(t *testing.T) {
	cfg := DefaultAudiobookConfig()
	cfg.RequestTimeoutSeconds = 42
	engineCfg := AudiobookEngineConfig{BaseURL: "https://example.invalid", APIKey: "k"}

	got := cfg.ttsConfig(engineCfg, cfg.requestTimeout())

	if got.BaseURL != engineCfg.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, engineCfg.BaseURL)
	}
	if got.APIKey != engineCfg.APIKey {
		t.Errorf("APIKey = %q, want %q", got.APIKey, engineCfg.APIKey)
	}
	if want := 42 * time.Second; got.Timeout != want {
		t.Errorf("Timeout = %v, want %v", got.Timeout, want)
	}
}

func TestAudiobookProbeUsesShortTimeout(t *testing.T) {
	cfg := DefaultAudiobookConfig()
	cfg.RequestTimeoutSeconds = 600

	got := cfg.ttsConfig(AudiobookEngineConfig{BaseURL: "https://example.invalid"}, probeTimeout)

	if got.Timeout != probeTimeout {
		t.Errorf("probe timeout = %v, want %v", got.Timeout, probeTimeout)
	}
	if probeTimeout >= tts.DefaultTimeout {
		t.Errorf("probe timeout %v should be shorter than the synthesis timeout %v", probeTimeout, tts.DefaultTimeout)
	}
}
