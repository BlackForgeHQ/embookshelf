// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackforge/embookshelf/internal/llm"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/tts"
)

// stubChat answers a chat-completions call and records the auth headers it
// was sent, so a test can assert what actually reached the wire rather
// than what a config struct claimed.
func stubChat(t *testing.T, seen *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ready"}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The Azure regression: the row stores "api-key", and every caller that
// builds a client from it must present that header. The worker used to
// assemble its own config without the field, so llm.New defaulted to
// bearer and every generation 401'd against an Azure endpoint while the
// admin's connection test — which did pass the field — reported success.
func TestReadingGuideClientSendsConfiguredAuthStyle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		authStyle   string
		wantHeader  string
		emptyHeader string
	}{
		{name: "api-key", authStyle: "api-key", wantHeader: "Api-Key", emptyHeader: "Authorization"},
		{name: "bearer", authStyle: "bearer", wantHeader: "Authorization", emptyHeader: "Api-Key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen http.Header
			srv := stubChat(t, &seen)

			cfg := repo.ReadingGuideConfig{
				Enabled:   true,
				BaseURL:   srv.URL,
				Model:     "gpt-4o-mini",
				APIKey:    "secret",
				AuthStyle: tc.authStyle,
			}

			for _, build := range map[string]func() (*llm.Client, error){
				"Client":      cfg.Client,
				"ProbeClient": cfg.ProbeClient,
			} {
				client, err := build()
				if err != nil {
					t.Fatalf("build client: %v", err)
				}
				if _, err := client.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}); err != nil {
					t.Fatalf("Chat: %v", err)
				}
				if got := seen.Get(tc.wantHeader); got == "" {
					t.Errorf("%s header is empty, want the credential", tc.wantHeader)
				}
				if got := seen.Get(tc.emptyHeader); got != "" {
					t.Errorf("%s header = %q, want empty", tc.emptyHeader, got)
				}
			}
		})
	}
}

func TestReadingGuideClientRefusesIncompleteRow(t *testing.T) {
	if _, err := (repo.ReadingGuideConfig{Enabled: true, Model: "m"}).Client(); err == nil {
		t.Error("want an error for a row with no base URL")
	}
}

func TestAudiobookSelectEngineAnswersEverythingACallerNeeds(t *testing.T) {
	cfg := repo.DefaultAudiobookConfig()
	cfg.Enabled = true
	cfg.Engine = string(tts.EngineOpenAI)
	slot := cfg.EngineSlot(tts.EngineOpenAI)
	slot.Enabled = true
	slot.BaseURL = "https://example.invalid/v1"
	slot.DefaultVoice = "alloy"

	sel, err := cfg.SelectEngine()
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	if sel.ID != tts.EngineOpenAI {
		t.Errorf("ID = %q, want %q", sel.ID, tts.EngineOpenAI)
	}
	if sel.Engine == nil {
		t.Error("Engine is nil, want an adapter")
	}
	if sel.Settings.DefaultVoice != "alloy" {
		t.Errorf("Settings.DefaultVoice = %q, want alloy", sel.Settings.DefaultVoice)
	}
	// The per-request cap travels with the selection, so a caller never
	// has to reach back into the catalog to use the engine correctly.
	if sel.Info.MaxRequestChars <= 0 {
		t.Errorf("Info.MaxRequestChars = %d, want the catalog cap", sel.Info.MaxRequestChars)
	}
}

func TestAudiobookSelectEngineRejectsUnknownEngine(t *testing.T) {
	cfg := repo.DefaultAudiobookConfig()
	cfg.Engine = "nope"

	if _, err := cfg.SelectEngine(); err == nil {
		t.Error("want an error for an engine that is not in the catalog")
	}
}
