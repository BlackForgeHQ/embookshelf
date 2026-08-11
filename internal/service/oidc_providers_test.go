// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
)

// The registry is the one place a provider exists (#258): its slug, its
// login-page entry, its dispatch, and its connection test all come from
// the same entry. These tests enumerate the registry rather than naming
// slugs one by one, so a fourth provider is covered the moment its entry
// is added.

// Every registered provider, once its config is usable, appears on the
// login page — in registry order, with a login URL derived from its slug.
// The listing is compared against the registry itself, not a hand-kept
// list, which is what pins "adding a provider is one entry".
func TestPublicListingDerivesFromTheRegistry(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{
		google:  repo.OAuthPresetConfig{Enabled: true, ClientID: "g", ClientSecret: "s"},
		github:  repo.OAuthPresetConfig{Enabled: true, ClientID: "h", ClientSecret: "s"},
		generic: repo.GenericOIDCConfig{Enabled: true, ClientID: "c", IssuerURI: "https://idp.test", ProviderName: "Corp"},
	})

	got, err := svc.publicProviders(context.Background())
	if err != nil {
		t.Fatalf("publicProviders: %v", err)
	}

	if len(got) != len(svc.providers) {
		t.Fatalf("listed %d providers, registry has %d — the listing is not registry-derived", len(got), len(svc.providers))
	}
	for i, entry := range svc.providers {
		p := got[i]
		if p.Slug != entry.slug {
			t.Errorf("listing[%d].Slug = %q, want registry slug %q", i, p.Slug, entry.slug)
		}
		if p.LoginURL != "/api/v1/auth/oidc/"+entry.slug {
			t.Errorf("listing[%d].LoginURL = %q, want it derived from the slug", i, p.LoginURL)
		}
		if p.Name == "" || p.Kind == "" {
			t.Errorf("listing[%d] = %+v, want a name and a kind", i, p)
		}
	}
}

// A generic provider with no display name still needs a button label.
func TestPublicListingDefaultsGenericNameToSSO(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{
		generic: repo.GenericOIDCConfig{Enabled: true, ClientID: "c", IssuerURI: "https://idp.test"},
	})

	got, err := svc.publicProviders(context.Background())
	if err != nil {
		t.Fatalf("publicProviders: %v", err)
	}
	if len(got) != 1 || got[0].Name != "SSO" {
		t.Fatalf("listing = %+v, want a single provider named SSO", got)
	}
}

// The connection test dispatches through the same registry as the login
// flow, so an unknown slug is the same refusal everywhere.
func TestTestProviderRejectsUnknownSlug(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{})
	_, err := svc.TestProvider(context.Background(), "myspace", nil)
	if !errors.Is(err, ErrOIDCUnknownProvider) {
		t.Errorf("err = %v, want ErrOIDCUnknownProvider", err)
	}
}

// The blank-submission rule, stated once: an override that is missing its
// required fields falls back to the stored row, and a failure to read
// that row is the caller's error, not a diagnostic verdict.
func TestBlankSubmissionFallsBackToStoredRow(t *testing.T) {
	t.Parallel()

	stored := repo.OAuthPresetConfig{ClientID: "stored-id"}
	blank := func(c repo.OAuthPresetConfig) bool { return c.ClientID == "" }
	run := func(_ context.Context, c repo.OAuthPresetConfig) TestResult {
		return TestResult{Checks: []TestCheck{{Name: c.ClientID}}}
	}

	t.Run("a filled override wins", func(t *testing.T) {
		t.Parallel()
		res, err := testWithStoredFallback(context.Background(),
			repo.OAuthPresetConfig{ClientID: "typed-id"}, blank,
			func(context.Context) (repo.OAuthPresetConfig, error) {
				t.Fatal("stored row read for a non-blank submission")
				return repo.OAuthPresetConfig{}, nil
			}, run)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if res.Checks[0].Name != "typed-id" {
			t.Errorf("ran against %q, want the typed override", res.Checks[0].Name)
		}
	})

	t.Run("a blank override falls back", func(t *testing.T) {
		t.Parallel()
		res, err := testWithStoredFallback(context.Background(),
			repo.OAuthPresetConfig{}, blank,
			func(context.Context) (repo.OAuthPresetConfig, error) { return stored, nil }, run)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if res.Checks[0].Name != "stored-id" {
			t.Errorf("ran against %q, want the stored row", res.Checks[0].Name)
		}
	})

	t.Run("a stored-row failure surfaces", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("boom")
		_, err := testWithStoredFallback(context.Background(),
			repo.OAuthPresetConfig{}, blank,
			func(context.Context) (repo.OAuthPresetConfig, error) { return repo.OAuthPresetConfig{}, wantErr }, run)
		if !errors.Is(err, wantErr) {
			t.Errorf("err = %v, want the store failure", err)
		}
	})
}

// conformingIssuer serves a minimal well-formed discovery document, so a
// diagnostic run against it passes every critical check.
func conformingIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                   srv.URL,
			"authorization_endpoint":   srv.URL + "/authorize",
			"token_endpoint":           srv.URL + "/token",
			"jwks_uri":                 srv.URL + "/jwks",
			"scopes_supported":         []string{"openid", "profile", "email"},
			"response_types_supported": []string{"code"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"k1"}]}`))
	})
	return srv
}

// A submission carrying a config runs against that config; one without
// runs against the stored row. This is the end-to-end shape the panel's
// "test before saving" relies on, pinned against a stub issuer.
func TestGenericConnectionTestPrefersTheSubmission(t *testing.T) {
	t.Parallel()

	srv := conformingIssuer(t)
	svc := oidcForTest(t, &fakeOIDCSettings{
		generic: repo.GenericOIDCConfig{Enabled: true, ClientID: "stored", IssuerURI: srv.URL},
	})

	// Blank body → stored row → the stub issuer answers.
	res, err := svc.TestProvider(context.Background(), repo.ProviderSlugGeneric, []byte(`{}`))
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if !res.Success {
		t.Fatalf("stored-row fallback failed against a conforming issuer: %+v", res.Checks)
	}

	// A typed submission wins over the stored row: an unreachable issuer
	// in the body must fail even though the stored one would pass.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	body, _ := json.Marshal(map[string]any{
		"generic": map[string]any{"clientId": "typed", "issuerUri": deadURL},
	})
	res, err = svc.TestProvider(context.Background(), repo.ProviderSlugGeneric, body)
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if res.Success {
		t.Fatal("a typed submission naming a dead issuer passed — the stored row was tested instead")
	}
}
