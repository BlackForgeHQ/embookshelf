// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

func converterSettingsRepo(t *testing.T) (*repo.AppSettingsRepo, context.Context) {
	t.Helper()
	return repo.NewAppSettingsRepo(repotest.New(t), testCipher(t)), context.Background()
}

// TestConverterConfigDefaults — a missing row reads as "not configured",
// not an error: the extension is optional and most installs never run it.
func TestConverterConfigDefaults(t *testing.T) {
	r, ctx := converterSettingsRepo(t)

	cfg, err := r.GetConverter(ctx)
	if err != nil {
		t.Fatalf("GetConverter on a missing row: %v", err)
	}
	if cfg.Enabled {
		t.Error("enabled by default — the sidecar is opt-in")
	}
	if cfg.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty by default", cfg.BaseURL)
	}
}

func TestConverterConfigRoundTrip(t *testing.T) {
	r, ctx := converterSettingsRepo(t)

	want := repo.ConverterConfig{Enabled: true, BaseURL: "http://converter:6070"}
	if err := r.SetConverter(ctx, want); err != nil {
		t.Fatalf("SetConverter: %v", err)
	}
	got, err := r.GetConverter(ctx)
	if err != nil {
		t.Fatalf("GetConverter: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestConverterNormalisesBaseURL — operators paste URLs with whitespace
// and trailing slashes; "/convert" appended to "…:6070/" would double the
// slash.
func TestConverterNormalisesBaseURL(t *testing.T) {
	r, ctx := converterSettingsRepo(t)

	if err := r.SetConverter(ctx, repo.ConverterConfig{BaseURL: "  http://converter:6070/  "}); err != nil {
		t.Fatalf("SetConverter: %v", err)
	}
	got, err := r.GetConverter(ctx)
	if err != nil {
		t.Fatalf("GetConverter: %v", err)
	}
	if got.BaseURL != "http://converter:6070" {
		t.Fatalf("BaseURL = %q, want trimmed with no trailing slash", got.BaseURL)
	}
}

// TestConverterRejectsEnabledWithoutURL — enabling with no URL ships a
// feature that fails on every conversion. Refuse at save time, where the
// admin can see why.
func TestConverterRejectsEnabledWithoutURL(t *testing.T) {
	r, ctx := converterSettingsRepo(t)

	if err := r.SetConverter(ctx, repo.ConverterConfig{Enabled: true}); err == nil {
		t.Fatal("accepted an enabled config with no URL")
	}
}

// TestConverterRejectsNonHTTPURL — the URL is dialed by the server; a
// value that does not parse as http(s) can only ever produce confusing
// runtime errors.
func TestConverterRejectsNonHTTPURL(t *testing.T) {
	r, ctx := converterSettingsRepo(t)

	for _, u := range []string{"converter:6070", "ftp://converter", "not a url at all"} {
		if err := r.SetConverter(ctx, repo.ConverterConfig{Enabled: true, BaseURL: u}); err == nil {
			t.Fatalf("accepted BaseURL = %q", u)
		}
	}
}

// TestConverterAllowsIncompleteWhileDisabled — an admin fills the form in
// stages; only enabling demands completeness.
func TestConverterAllowsIncompleteWhileDisabled(t *testing.T) {
	r, ctx := converterSettingsRepo(t)

	if err := r.SetConverter(ctx, repo.ConverterConfig{}); err != nil {
		t.Fatalf("SetConverter: %v", err)
	}
}

// TestConverterConfigured — the one statement of "is the extension
// usable": enabled with a URL. Everything that used to restate
// `!cfg.Enabled || cfg.BaseURL == ""` inline consults this (#298).
func TestConverterConfigured(t *testing.T) {
	cases := map[string]struct {
		cfg  repo.ConverterConfig
		want bool
	}{
		"enabled with URL":    {repo.ConverterConfig{Enabled: true, BaseURL: "http://converter:6070"}, true},
		"disabled with URL":   {repo.ConverterConfig{Enabled: false, BaseURL: "http://converter:6070"}, false},
		"enabled without URL": {repo.ConverterConfig{Enabled: true}, false},
		"zero value":          {repo.ConverterConfig{}, false},
	}
	for name, tc := range cases {
		if got := tc.cfg.Configured(); got != tc.want {
			t.Errorf("%s: Configured() = %v, want %v", name, got, tc.want)
		}
	}
}
