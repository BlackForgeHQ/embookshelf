// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func guideSettingsRepo(t *testing.T) (*repo.AppSettingsRepo, context.Context) {
	t.Helper()
	return repo.NewAppSettingsRepo(repotest.New(t), testCipher(t)), context.Background()
}

// TestReadingGuideConfigDefaults — a missing row must read as "not
// configured" rather than an error, so first boot works with no seed and
// the settings panel has something to render.
func TestReadingGuideConfigDefaults(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	cfg, err := r.GetReadingGuide(ctx)
	if err != nil {
		t.Fatalf("GetReadingGuide on a missing row: %v", err)
	}
	if cfg.Enabled {
		t.Error("enabled by default — generation must be opt-in, it costs money")
	}
	if cfg.Language == "" {
		t.Error("Language default is empty; the prompt needs one")
	}
	if cfg.TextCap <= 0 {
		t.Errorf("TextCap = %d, want a positive default", cfg.TextCap)
	}
	if cfg.AuthStyle != "bearer" {
		t.Errorf("AuthStyle = %q, want bearer by default", cfg.AuthStyle)
	}
}

// TestReadingGuideAuthStyleFallsBackToBearer — anything unrecognised must
// not silently produce a request with no credential header at all.
func TestReadingGuideAuthStyleFallsBackToBearer(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	if err := r.SetReadingGuide(ctx, repo.ReadingGuideConfig{AuthStyle: "nonsense"}); err != nil {
		t.Fatalf("SetReadingGuide: %v", err)
	}
	got, err := r.GetReadingGuide(ctx)
	if err != nil {
		t.Fatalf("GetReadingGuide: %v", err)
	}
	if got.AuthStyle != "bearer" {
		t.Fatalf("AuthStyle = %q, want bearer", got.AuthStyle)
	}
}

func TestReadingGuideConfigRoundTrip(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	want := repo.ReadingGuideConfig{
		Enabled:         true,
		BaseURL:         "https://api.openai.com/v1",
		Model:           "gpt-4o-mini",
		APIKey:          "sk-secret-value",
		AuthStyle:       "api-key",
		Language:        "ru",
		TextCap:         96_000,
		RequestJSONMode: true,
	}
	if err := r.SetReadingGuide(ctx, want); err != nil {
		t.Fatalf("SetReadingGuide: %v", err)
	}

	got, err := r.GetReadingGuide(ctx)
	if err != nil {
		t.Fatalf("GetReadingGuide: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestReadingGuideAPIKeyEncryptedAtRest — the key is a credential like
// the SMTP password and the provider tokens (ADR-0010). It must not sit
// in the row as plaintext.
func TestReadingGuideAPIKeyEncryptedAtRest(t *testing.T) {
	d := repotest.New(t)
	r := repo.NewAppSettingsRepo(d, testCipher(t))
	ctx := context.Background()

	const secret = "sk-super-secret-do-not-store-raw"
	if err := r.SetReadingGuide(ctx, repo.ReadingGuideConfig{
		BaseURL: "https://x/v1", Model: "m", APIKey: secret,
	}); err != nil {
		t.Fatalf("SetReadingGuide: %v", err)
	}

	raw, err := r.GetRaw(ctx, repo.SettingReadingGuide)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("API key stored in plaintext: %s", raw)
	}

	got, err := r.GetReadingGuide(ctx)
	if err != nil {
		t.Fatalf("GetReadingGuide: %v", err)
	}
	if got.APIKey != secret {
		t.Fatalf("APIKey = %q, want it decrypted back", got.APIKey)
	}
}

// TestReadingGuideNormalises — operators paste URLs with trailing slashes
// and language codes with stray case and spaces.
func TestReadingGuideNormalises(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	if err := r.SetReadingGuide(ctx, repo.ReadingGuideConfig{
		BaseURL:  "  https://api.openai.com/v1/  ",
		Model:    "  gpt-4o-mini ",
		Language: "  RU ",
	}); err != nil {
		t.Fatalf("SetReadingGuide: %v", err)
	}

	got, err := r.GetReadingGuide(ctx)
	if err != nil {
		t.Fatalf("GetReadingGuide: %v", err)
	}
	if got.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", got.BaseURL)
	}
	if got.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q", got.Model)
	}
	if got.Language != "ru" {
		t.Errorf("Language = %q, want lowercased", got.Language)
	}
}

// TestReadingGuideRejectsEnabledWithoutEndpoint — enabling with no base
// URL or model produces a feature that fails on every click. Refuse at
// save time, where the admin can see why.
func TestReadingGuideRejectsEnabledWithoutEndpoint(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	for name, cfg := range map[string]repo.ReadingGuideConfig{
		"no base url": {Enabled: true, Model: "m"},
		"no model":    {Enabled: true, BaseURL: "https://x/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.SetReadingGuide(ctx, cfg); err == nil {
				t.Fatal("accepted an enabled config with no endpoint")
			}
		})
	}
}

// TestReadingGuideAllowsIncompleteWhileDisabled — an admin fills the form
// in stages; only enabling it demands completeness.
func TestReadingGuideAllowsIncompleteWhileDisabled(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	if err := r.SetReadingGuide(ctx, repo.ReadingGuideConfig{BaseURL: "https://x/v1"}); err != nil {
		t.Fatalf("SetReadingGuide: %v", err)
	}
}

// TestReadingGuideClampsTextCap — the cap is the cost dial; zero or
// negative would mean "send the whole book" on a field an admin can type
// into freely.
func TestReadingGuideClampsTextCap(t *testing.T) {
	r, ctx := guideSettingsRepo(t)

	for _, in := range []int64{0, -1} {
		if err := r.SetReadingGuide(ctx, repo.ReadingGuideConfig{TextCap: in}); err != nil {
			t.Fatalf("SetReadingGuide(%d): %v", in, err)
		}
		got, err := r.GetReadingGuide(ctx)
		if err != nil {
			t.Fatalf("GetReadingGuide: %v", err)
		}
		if got.TextCap <= 0 {
			t.Fatalf("TextCap(%d) stored as %d, want a positive default", in, got.TextCap)
		}
	}
}
