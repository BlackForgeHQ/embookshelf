// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// fakeOIDCRows is the row store behind service.OIDCSettingsService,
// committing an applied submission back into the fake settings store so
// the endpoint's read-after-write is a real one.
//
// Prepare and commit stay separate here for the same reason they are
// separate in production: the service validates and prepares before it
// writes anything, and a fake that collapsed the two would let a refused
// submission look like it had been persisted.
type fakeOIDCRows struct {
	store   *fakeAppSettings
	pending *service.OIDCSubmission
	commits int
}

func (f *fakeOIDCRows) PrepareOIDCRows(sub service.OIDCSubmission) ([]repo.SettingRow, error) {
	f.pending = &sub
	return []repo.SettingRow{{Name: repo.SettingOIDCForceOnlyMode}}, nil
}

func (f *fakeOIDCRows) SetRows(context.Context, []repo.SettingRow) error {
	if f.pending == nil {
		return errBoom
	}
	f.store.google = f.pending.Google
	f.store.github = f.pending.GitHub
	f.store.generic = f.pending.Generic
	f.store.autoProvis = f.pending.AutoProvision
	if f.store.bools == nil {
		f.store.bools = map[string]bool{}
	}
	f.store.bools[repo.SettingOIDCForceOnlyMode] = f.pending.ForceOnly
	f.commits++
	return nil
}

// newOIDCTestHandler wires the OIDC settings surface over fakes only.
func newOIDCTestHandler(store *fakeAppSettings, appURL string) (*Handler, *fakeOIDCRows) {
	rows := &fakeOIDCRows{store: store}
	return &Handler{
		appSettings:  store,
		oidcSettings: service.NewOIDCSettingsService(rows, nil),
		cfg:          config.Config{AppURL: appURL},
	}, rows
}

// TestSettingsOIDCGetNeverReturnsAClientSecret — three providers, three
// secrets, and the panel is allowed to say only that each one exists.
func TestSettingsOIDCGetNeverReturnsAClientSecret(t *testing.T) {
	store := &fakeAppSettings{
		google:  repo.OAuthPresetConfig{Enabled: true, ClientID: "g-id", ClientSecret: "g-secret"},
		github:  repo.OAuthPresetConfig{ClientID: "gh-id", ClientSecret: "gh-secret"},
		generic: repo.GenericOIDCConfig{ClientID: "gen-id", ClientSecret: "gen-secret", IssuerURI: "https://idp.example.com"},
		bools:   map[string]bool{repo.SettingOIDCForceOnlyMode: true},
	}
	h, _ := newOIDCTestHandler(store, "https://books.example.com")

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/oidc", "")
	h.SettingsOIDCGet(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"g-secret", "gh-secret", "gen-secret"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("client secret %q travelled to the client: %s", secret, rec.Body.String())
		}
	}

	var got oidcSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if !got.ForceOnly {
		t.Error("forceOnly lost")
	}
	if !got.Google.ClientSecretSet || !got.GitHub.ClientSecretSet || !got.Generic.ClientSecretSet {
		t.Errorf("a stored secret was reported absent: %+v", got)
	}
	if got.Google.ClientID != "g-id" || got.Generic.IssuerURI != "https://idp.example.com" {
		t.Errorf("non-secret fields lost: %+v", got)
	}
	if got.RedirectURI != "https://books.example.com/api/v1/auth/oidc/callback" {
		t.Errorf("redirectUri = %q", got.RedirectURI)
	}
}

// TestSettingsOIDCUpdateResolvesEverySecret walks the tri-state rule
// across all three providers at once, which is the shape the panel
// actually submits: one save carrying three credential fields, only one
// of which the admin retyped.
func TestSettingsOIDCUpdateResolvesEverySecret(t *testing.T) {
	store := &fakeAppSettings{
		google:  repo.OAuthPresetConfig{Enabled: true, ClientID: "g-id", ClientSecret: "g-secret"},
		github:  repo.OAuthPresetConfig{Enabled: true, ClientID: "gh-id", ClientSecret: "gh-secret"},
		generic: repo.GenericOIDCConfig{ClientID: "gen-id", ClientSecret: "gen-secret"},
	}
	h, rows := newOIDCTestHandler(store, "https://books.example.com")

	// Google: retyped. GitHub: left blank with the flag up (keep).
	// Generic: left blank with the flag down (clear).
	body := `{
		"forceOnly": false,
		"autoProvision": {"defaultRole":"user"},
		"google":  {"enabled":true,"clientId":" g-id ","clientSecret":"g-new","clientSecretSet":true},
		"github":  {"enabled":true,"clientId":"gh-id","clientSecret":"","clientSecretSet":true},
		"generic": {"enabled":false,"clientId":"gen-id","clientSecret":"","clientSecretSet":false}
	}`
	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/oidc", body)
	h.SettingsOIDCUpdate(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rows.commits != 1 {
		t.Fatalf("committed %d times, want 1", rows.commits)
	}
	if store.google.ClientSecret != "g-new" {
		t.Errorf("google secret = %q, want the retyped one", store.google.ClientSecret)
	}
	if store.github.ClientSecret != "gh-secret" {
		t.Errorf("github secret = %q, want the stored one kept", store.github.ClientSecret)
	}
	if store.generic.ClientSecret != "" {
		t.Errorf("generic secret = %q, want it cleared", store.generic.ClientSecret)
	}
	// The client id is trimmed on the way in; a pasted value with a
	// trailing space is a client_id the IdP will not recognise.
	if store.google.ClientID != "g-id" {
		t.Errorf("clientId = %q, want it trimmed", store.google.ClientID)
	}
}

// TestSettingsOIDCUpdateIncompleteProviderIsA400 — enabling a provider
// without the credentials a login needs is the admin's mistake, and the
// message names which provider to fix.
func TestSettingsOIDCUpdateIncompleteProviderIsA400(t *testing.T) {
	store := &fakeAppSettings{}
	h, rows := newOIDCTestHandler(store, "")

	body := `{"google":{"enabled":true,"clientId":"g-id","clientSecret":"","clientSecretSet":false}}`
	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/oidc", body)
	h.SettingsOIDCUpdate(c)

	if httpStatus(c, rec) != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if rows.commits != 0 {
		t.Error("a refused submission was still committed")
	}
}

// TestBuildRedirectURIEdges covers the two arms of the redirect-URI
// builder the origin tests in oidc_test.go leave open.
//
// The string an admin copies into their IdP has to equal what the login
// flow sends, byte for byte, or the IdP answers redirect_uri_mismatch
// and nothing inside embookshelf explains why. TestRedirectURIPrefersAppURL
// and TestRequestOriginProxyHeaders already pin the APP_URL-versus-proxy
// precedence; what neither reaches is how APP_URL is normalised, and
// what happens when there is no origin to be had.
func TestBuildRedirectURIEdges(t *testing.T) {
	const path = "/api/v1/auth/oidc/callback"

	cases := []struct {
		name   string
		appURL string
		host   string
		tls    bool
		want   string
	}{
		{
			// A trailing slash is what an admin pastes out of a browser
			// bar, and a doubled slash is a different string to an IdP.
			name:   "a trailing slash on APP_URL does not double up",
			appURL: "https://books.example.com/",
			host:   "internal.local:6060",
			want:   "https://books.example.com" + path,
		},
		{
			name: "a TLS request with no APP_URL yields https",
			host: "books.example.com",
			tls:  true,
			want: "https://books.example.com" + path,
		},
		{
			// Neither APP_URL nor a host: no honest answer exists, and an
			// empty string beats a URI the admin registers and never matches.
			name: "no APP_URL and no host yields nothing",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{cfg: config.Config{AppURL: tc.appURL}}
			c, _ := settingsCtx(t, http.MethodGet, "/api/v1/settings/oidc", "")
			c.Request.Host = tc.host
			if tc.tls {
				c.Request.TLS = &tls.ConnectionState{}
			}
			if got := h.buildRedirectURI(c); got != tc.want {
				t.Errorf("buildRedirectURI = %q, want %q", got, tc.want)
			}
		})
	}
}
