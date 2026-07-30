// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/tts"
)

func TestAudiobookNormalizeTrimsAndDefaults(t *testing.T) {
	t.Parallel()

	got := audiobookSetting.Normalize(AudiobookConfig{
		Engine: "  OpenAI  ",
		OpenAI: AudiobookEngineConfig{
			BaseURL:      " https://api.openai.com/v1/ ",
			APIKey:       "  sk-test  ",
			Model:        " tts-1 ",
			DefaultVoice: " alloy ",
		},
	})

	if got.Engine != "openai" {
		t.Errorf("Engine = %q, want openai", got.Engine)
	}
	// A trailing slash plus "/audio/speech" produces a double slash, which
	// some gateways 404 on.
	if got.OpenAI.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q, want no trailing slash", got.OpenAI.BaseURL)
	}
	if got.OpenAI.APIKey != "sk-test" || got.OpenAI.Model != "tts-1" || got.OpenAI.DefaultVoice != "alloy" {
		t.Errorf("fields not trimmed: %+v", got.OpenAI)
	}
}

// A zero or negative segment size would mean "send the whole book as one
// job", which is the one value this field must never take by accident —
// it is what keeps a job inside River's rescue window.
func TestAudiobookNormalizeRefusesANonPositiveSegmentSize(t *testing.T) {
	t.Parallel()

	for _, in := range []int{0, -1, -40000} {
		got := audiobookSetting.Normalize(AudiobookConfig{SegmentChars: in})
		if got.SegmentChars != DefaultAudiobookSegmentChars {
			t.Errorf("SegmentChars(%d) = %d, want the default %d",
				in, got.SegmentChars, DefaultAudiobookSegmentChars)
		}
	}
}

// Free is a real price: a self-hoster pointing at a local Kokoro pays
// nothing, and forcing the catalog default on them would show an
// estimate of $8 for a run that costs $0.
func TestAudiobookNormalizeKeepsAZeroPriceButRejectsNegative(t *testing.T) {
	t.Parallel()

	zero := audiobookSetting.Normalize(AudiobookConfig{
		OpenAI: AudiobookEngineConfig{PricePerMillionChars: 0},
	})
	if zero.OpenAI.PricePerMillionChars != 0 {
		t.Errorf("a zero price was overwritten with %v", zero.OpenAI.PricePerMillionChars)
	}

	negative := audiobookSetting.Normalize(AudiobookConfig{
		OpenAI: AudiobookEngineConfig{PricePerMillionChars: -5},
	})
	info, _ := tts.Lookup(tts.EngineOpenAI)
	if negative.OpenAI.PricePerMillionChars != info.DefaultPricePerMillionChars {
		t.Errorf("negative price = %v, want the catalog default %v",
			negative.OpenAI.PricePerMillionChars, info.DefaultPricePerMillionChars)
	}
}

// An admin fills the form in stages, so a disabled row is never refused —
// but enabling one that cannot possibly work ships a button that fails on
// every click.
func TestAudiobookValidateOnlyBindsWhenEnabled(t *testing.T) {
	t.Parallel()

	if err := audiobookSetting.Validate(AudiobookConfig{Enabled: false}); err != nil {
		t.Fatalf("a disabled half-filled row must be saveable, got %v", err)
	}
}

func TestAudiobookValidateRequiresAUsableSelectedEngine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  AudiobookConfig
		want string
	}{
		{
			name: "no engine selected",
			cfg:  AudiobookConfig{Enabled: true},
			want: "engine",
		},
		{
			name: "engine outside the catalog",
			cfg:  AudiobookConfig{Enabled: true, Engine: "polly"},
			want: "engine",
		},
		{
			name: "selected engine has no voice",
			cfg: AudiobookConfig{Enabled: true, Engine: "openai", OpenAI: AudiobookEngineConfig{
				Enabled: true, BaseURL: "https://api.openai.com/v1", Model: "tts-1",
			}},
			want: "voice",
		},
		{
			name: "selected engine is itself disabled",
			cfg: AudiobookConfig{Enabled: true, Engine: "openai", OpenAI: AudiobookEngineConfig{
				Enabled: false, BaseURL: "https://x", Model: "tts-1", DefaultVoice: "alloy",
			}},
			want: "enabled",
		},
		{
			name: "elevenlabs without a key",
			cfg: AudiobookConfig{Enabled: true, Engine: "elevenlabs", ElevenLabs: AudiobookEngineConfig{
				Enabled: true, Model: "eleven_multilingual_v2", DefaultVoice: "v1",
			}},
			want: "key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := audiobookSetting.Validate(audiobookSetting.Normalize(tc.cfg))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got nil", tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A local OpenAI-compatible server needs no key, and demanding one would
// block the configuration that costs nothing and keeps the book on the
// operator's own machine.
func TestAudiobookValidateAcceptsAKeylessLocalEngine(t *testing.T) {
	t.Parallel()

	cfg := audiobookSetting.Normalize(AudiobookConfig{
		Enabled: true,
		Engine:  "openai",
		OpenAI: AudiobookEngineConfig{
			Enabled:      true,
			BaseURL:      "http://localhost:8880/v1",
			Model:        "kokoro",
			DefaultVoice: "af_sky",
		},
	})
	if err := audiobookSetting.Validate(cfg); err != nil {
		t.Fatalf("a keyless local engine must be valid, got %v", err)
	}
}

// The selected engine is read on every generate, so resolving it has to
// be one call rather than a switch repeated at each site.
func TestSelectedEngineReturnsTheChosenEntry(t *testing.T) {
	t.Parallel()

	cfg := AudiobookConfig{Engine: "elevenlabs", ElevenLabs: AudiobookEngineConfig{
		Enabled: true, APIKey: "xi", DefaultVoice: "v1", Model: "eleven_multilingual_v2",
	}}
	id, engine, err := cfg.SelectedEngine()
	if err != nil {
		t.Fatalf("SelectedEngine: %v", err)
	}
	if id != tts.EngineElevenLabs {
		t.Errorf("id = %q, want elevenlabs", id)
	}
	if engine.DefaultVoice != "v1" {
		t.Errorf("engine = %+v, want the elevenlabs sub-struct", engine)
	}

	// A sentinel, and one that names the id: the handler answers this 503
	// with the disabled code, and the only thing telling the admin which
	// id to fix is the message (#274).
	_, _, err = (AudiobookConfig{Engine: "nope"}).SelectedEngine()
	if !errors.Is(err, ErrUnknownAudiobookEngine) {
		t.Errorf("err = %v, want ErrUnknownAudiobookEngine for an engine outside the catalog", err)
	}
	if err != nil && !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v, want the engine named", err)
	}
}

// Adding a TTS engine means touching the catalog, its adapter, and this
// settings row's per-engine slot. The first two are one declaration
// after #183; this is the third site, and it cannot join them without
// changing the shape of persisted settings. So it fails loudly instead.
func TestEveryCatalogEngineHasASettingsSlot(t *testing.T) {
	var cfg AudiobookConfig
	for _, info := range tts.Catalog {
		if (&cfg).EngineSlot(info.ID) == nil {
			t.Errorf("engine %q is in the TTS catalog but has no settings slot — "+
				"add one to AudiobookConfig and to EngineSlot's switch", info.ID)
		}
	}
}

// The default row must be saveable and disabled: nothing should be able
// to spend money before an admin has said so.
func TestDefaultAudiobookConfigIsDisabledAndValid(t *testing.T) {
	t.Parallel()

	cfg := DefaultAudiobookConfig()
	if cfg.Enabled {
		t.Error("audiobook generation must be off by default")
	}
	if err := audiobookSetting.Validate(audiobookSetting.Normalize(cfg)); err != nil {
		t.Fatalf("the seeded default row must validate, got %v", err)
	}
	// Prices are prefilled from the catalog so the estimate is useful on
	// the first run rather than reading $0.00.
	info, _ := tts.Lookup(tts.EngineElevenLabs)
	if cfg.ElevenLabs.PricePerMillionChars != info.DefaultPricePerMillionChars {
		t.Errorf("elevenlabs price = %v, want the catalog default %v",
			cfg.ElevenLabs.PricePerMillionChars, info.DefaultPricePerMillionChars)
	}
}
