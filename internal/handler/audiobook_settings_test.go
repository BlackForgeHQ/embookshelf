// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/tts"
)

// storedAudiobook is a configured AUDIOBOOK row with a key in one slot.
func storedAudiobook() repo.AudiobookConfig {
	cfg := repo.DefaultAudiobookConfig()
	cfg.Enabled = true
	cfg.Engine = string(tts.EngineOpenAI)
	cfg.OpenAI.Enabled = true
	cfg.OpenAI.APIKey = "sk-stored"
	cfg.OpenAI.DefaultVoice = "alloy"
	return cfg
}

// TestSettingsAudiobookGetHidesEveryKey — same write-only contract as the
// SMTP password, over three engines instead of one. Only keySet travels.
func TestSettingsAudiobookGetHidesEveryKey(t *testing.T) {
	cfg := storedAudiobook()
	cfg.ElevenLabs.APIKey = "el-stored"
	h := &Handler{appSettings: &fakeAppSettings{audiobook: cfg}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/audiobook", "")
	h.SettingsAudiobookGet(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"sk-stored", "el-stored"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("engine key %q travelled to the client: %s", secret, rec.Body.String())
		}
	}

	var got audiobookSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	// The panel walks the catalog, not the config, so an engine nobody has
	// configured still renders — that is how an admin configures a new one.
	if len(got.Engines) != len(tts.Catalog) {
		t.Fatalf("rendered %d engines, want the whole catalog (%d)", len(got.Engines), len(tts.Catalog))
	}
	for _, e := range got.Engines {
		switch e.ID {
		case string(tts.EngineOpenAI), string(tts.EngineElevenLabs):
			if !e.KeySet {
				t.Errorf("%s reported no stored key despite one being set", e.ID)
			}
		case string(tts.EngineAzure):
			if e.KeySet {
				t.Errorf("azure reported a stored key it never had")
			}
		}
	}
}

// TestSettingsAudiobookUpdateKeepsAKeyNotRetyped is the tri-state rule on
// the surface where it bites hardest: three credential fields on one
// form, so a submit that dropped the un-retyped ones would silently
// disable engines the admin never touched.
func TestSettingsAudiobookUpdateKeepsAKeyNotRetyped(t *testing.T) {
	cases := []struct {
		name   string
		apiKey string
		keySet bool
		want   string
	}{
		{"keep", "", true, "sk-stored"},
		{"clear", "", false, ""},
		{"replace", "sk-new", true, "sk-new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAppSettings{audiobook: storedAudiobook()}
			h := &Handler{appSettings: store}

			req := audiobookSettingsRequest{
				Enabled: true, Engine: string(tts.EngineOpenAI),
				Engines: []audiobookEngineRequest{{
					ID: string(tts.EngineOpenAI), Enabled: true,
					DefaultVoice: "nova",
					APIKey:       tc.apiKey, KeySet: tc.keySet,
				}},
			}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/audiobook", string(raw))
			h.SettingsAudiobookUpdate(c)

			if httpStatus(c, rec) != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			if store.audioWrites != 1 {
				t.Fatalf("audiobook row written %d times, want 1", store.audioWrites)
			}
			if store.audiobook.OpenAI.APIKey != tc.want {
				t.Errorf("stored key = %q, want %q", store.audiobook.OpenAI.APIKey, tc.want)
			}
			if store.audiobook.OpenAI.DefaultVoice != "nova" {
				t.Errorf("the edit the admin made was lost: %+v", store.audiobook.OpenAI)
			}
			if strings.Contains(rec.Body.String(), "sk-") {
				t.Errorf("the PUT response echoed a key back: %s", rec.Body.String())
			}
		})
	}
}

// TestSettingsAudiobookUpdateIgnoresAnUnknownEngine — the request names
// engines by id, so a client (or an older UI) naming one this binary does
// not ship must not create a slot or fail the save.
func TestSettingsAudiobookUpdateIgnoresAnUnknownEngine(t *testing.T) {
	store := &fakeAppSettings{audiobook: storedAudiobook()}
	h := &Handler{appSettings: store}

	body := `{"enabled":true,"engine":"openai","engines":[{"id":"nope","enabled":true,"apiKey":"x"}]}`
	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/audiobook", body)
	h.SettingsAudiobookUpdate(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if store.audiobook.OpenAI.APIKey != "sk-stored" {
		t.Errorf("an unknown engine disturbed a real slot: %+v", store.audiobook.OpenAI)
	}
}

// TestSettingsAudiobookUpdateRefusalIsA400 — the row refuses a config it
// cannot narrate with, and that is the admin's to fix.
func TestSettingsAudiobookUpdateRefusalIsA400(t *testing.T) {
	store := &fakeAppSettings{audiobook: storedAudiobook(), setAudioErr: errBoom}
	h := &Handler{appSettings: store}

	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/audiobook",
		`{"enabled":true,"engine":"openai","engines":[]}`)
	h.SettingsAudiobookUpdate(c)

	if httpStatus(c, rec) != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestSettingsAudiobookProbeDegradesWhenDisabled — the voices and test
// endpoints must answer the catalogued AUDIOBOOKS_DISABLED code rather
// than a bare 503, because the UI switches on it to explain why the
// generate dialog is empty.
func TestSettingsAudiobookProbeDegradesWhenDisabled(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{audiobook: repo.DefaultAudiobookConfig()}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/audiobook/voices", "")
	h.SettingsAudiobookVoices(c)
	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("voices status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), CodeAudiobooksDisabled) {
		t.Errorf("body carries no %s code: %s", CodeAudiobooksDisabled, rec.Body.String())
	}
}
