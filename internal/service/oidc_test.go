// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/blackforge/embookshelf/internal/repo"
)

// ---------------------------------------------------------------------------
// stateStore — the CSRF/PKCE boundary
// ---------------------------------------------------------------------------
//
// stateStore is the only thing tying an authorize redirect to the callback
// that comes back: it holds the PKCE verifier, the nonce, the provider
// slug, and the exact redirect_uri. If a forged callback could find an
// entry, or a real one could reuse a consumed entry, the login flow would
// accept it. It is a plain map, mutex and clock with no I/O — the most
// obviously testable code in the package, and it had no tests at all.

func TestStateStoreRoundTrip(t *testing.T) {
	t.Parallel()
	s := newStateStore()

	want := stateEntry{
		Nonce:        "n-1",
		CodeVerifier: "v-1",
		CreatedAt:    time.Now(),
		ProviderSlug: repo.ProviderSlugGoogle,
		RedirectURL:  "https://app.example/callback",
		Intent:       IntentLogin,
	}
	s.put("state-1", want)

	got, ok := s.take("state-1")
	if !ok {
		t.Fatal("a freshly stored state was not found")
	}
	if got.CodeVerifier != want.CodeVerifier || got.Nonce != want.Nonce {
		t.Errorf("PKCE verifier/nonce did not survive: %+v", got)
	}
	if got.ProviderSlug != want.ProviderSlug || got.RedirectURL != want.RedirectURL {
		t.Errorf("routing fields did not survive: %+v", got)
	}
}

// A state is single-use. Replaying a callback must not find it again.
func TestStateStoreTakeConsumesTheEntry(t *testing.T) {
	t.Parallel()
	s := newStateStore()
	s.put("state-1", stateEntry{CreatedAt: time.Now()})

	if _, ok := s.take("state-1"); !ok {
		t.Fatal("first take should succeed")
	}
	if _, ok := s.take("state-1"); ok {
		t.Fatal("second take succeeded — a replayed callback would be accepted")
	}
}

// An unknown state is the forged-callback case.
func TestStateStoreRejectsUnknownState(t *testing.T) {
	t.Parallel()
	s := newStateStore()
	s.put("real", stateEntry{CreatedAt: time.Now()})

	if _, ok := s.take("forged"); ok {
		t.Fatal("an unknown state was accepted")
	}
}

// Entries older than stateTTL are not honoured, so a stale authorize
// redirect cannot be completed much later.
func TestStateStoreExpiresPastTTL(t *testing.T) {
	t.Parallel()
	s := newStateStore()

	s.put("old", stateEntry{CreatedAt: time.Now().Add(-2 * stateTTL)})
	if _, ok := s.take("old"); ok {
		t.Error("an entry older than stateTTL was honoured")
	}

	s.put("fresh", stateEntry{CreatedAt: time.Now().Add(-stateTTL / 2)})
	if _, ok := s.take("fresh"); !ok {
		t.Error("an entry within the TTL was dropped")
	}
}

// Reaping happens on write, not on a timer — so a long-idle process does
// not accumulate expired entries forever once traffic resumes.
func TestStateStoreReapsOnWrite(t *testing.T) {
	t.Parallel()
	s := newStateStore()

	for _, k := range []string{"a", "b", "c"} {
		s.put(k, stateEntry{CreatedAt: time.Now().Add(-2 * stateTTL)})
	}
	s.put("fresh", stateEntry{CreatedAt: time.Now()})

	s.mu.Lock()
	n := len(s.m)
	s.mu.Unlock()
	if n != 1 {
		t.Errorf("store holds %d entries after a write, want 1 — expired ones were not reaped", n)
	}
}

func TestStateStoreIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	s := newStateStore()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			key := string(rune('a' + i))
			s.put(key, stateEntry{CreatedAt: time.Now()})
			s.take(key)
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// ---------------------------------------------------------------------------
// PKCE
// ---------------------------------------------------------------------------

// The challenge must be the base64url-unpadded SHA-256 of the verifier —
// an IdP computes the same thing and rejects a mismatch, so a wrong
// encoding here breaks every login with that provider.
func TestPKCEChallengeIsS256OfTheVerifier(t *testing.T) {
	t.Parallel()

	const verifier = "test-verifier-value"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])

	if got := pkceChallengeS256(verifier); got != want {
		t.Errorf("challenge = %q, want %q", got, want)
	}
	// Unpadded is part of the spec; a trailing '=' is a rejection.
	if got := pkceChallengeS256(verifier); got != "" && got[len(got)-1] == '=' {
		t.Error("challenge is padded — RFC 7636 requires base64url without padding")
	}
}

func TestRandomStringIsUniqueAndURLSafe(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		v, err := randomString(32)
		if err != nil {
			t.Fatalf("randomString: %v", err)
		}
		if seen[v] {
			t.Fatal("randomString repeated a value — state would be guessable")
		}
		seen[v] = true
		for _, r := range v {
			if r == '+' || r == '/' || r == '=' {
				t.Errorf("value %q contains a character unsafe for a URL parameter", v)
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scopes
// ---------------------------------------------------------------------------

func TestSplitScopes(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"empty falls back to the default set": {
			in: "", want: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		"whitespace only falls back too": {
			in: "   ", want: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		"openid is prepended when absent": {
			in: "profile email", want: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		"existing openid is not duplicated": {
			in: "openid profile", want: []string{oidc.ScopeOpenID, "profile"},
		},
		"duplicates are collapsed": {
			in: "openid profile profile email", want: []string{oidc.ScopeOpenID, "profile", "email"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := splitScopes(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitScopes(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitScopes(%q) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestOrString(t *testing.T) {
	t.Parallel()

	if got := orString("primary", "fallback"); got != "primary" {
		t.Errorf("got %q, want primary", got)
	}
	if got := orString("   ", "fallback"); got != "fallback" {
		t.Errorf("blank-but-not-empty primary should fall back, got %q", got)
	}
	if got := orString("", "fallback"); got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

// ---------------------------------------------------------------------------
// Provider gating
// ---------------------------------------------------------------------------

// A provider is only offered on the login page when it is both enabled
// and actually configured — otherwise the button 500s on click.
func TestProviderUsableGates(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		cfg  repo.OAuthPresetConfig
		want bool
	}{
		"fully configured":  {repo.OAuthPresetConfig{Enabled: true, ClientID: "id", ClientSecret: "s"}, true},
		"disabled":          {repo.OAuthPresetConfig{ClientID: "id", ClientSecret: "s"}, false},
		"missing client id": {repo.OAuthPresetConfig{Enabled: true, ClientSecret: "s"}, false},
		"missing secret":    {repo.OAuthPresetConfig{Enabled: true, ClientID: "id"}, false},
		"enabled but empty": {repo.OAuthPresetConfig{Enabled: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// One gate for both preset providers — Google and GitHub
			// take the same shape and the same rule.
			if got := presetUsable(tc.cfg); got != tc.want {
				t.Errorf("presetUsable = %v, want %v", got, tc.want)
			}
		})
	}
}

// Generic OIDC needs an issuer instead of a secret — public clients are
// legitimate, an issuerless config is not.
func TestGenericUsableRequiresIssuer(t *testing.T) {
	t.Parallel()

	if genericUsable(repo.GenericOIDCConfig{Enabled: true, ClientID: "id"}) {
		t.Error("a generic provider with no issuer should not be offered")
	}
	if !genericUsable(repo.GenericOIDCConfig{Enabled: true, ClientID: "id", IssuerURI: "https://idp"}) {
		t.Error("issuer + client id + enabled should be usable")
	}
	if genericUsable(repo.GenericOIDCConfig{ClientID: "id", IssuerURI: "https://idp"}) {
		t.Error("a disabled provider should not be offered")
	}
}

// googleOIDCConfig adapts the two-field preset onto the generic shape.
// Getting the issuer wrong here points discovery at the wrong host.
func TestGoogleOIDCConfigUsesGoogleIssuer(t *testing.T) {
	t.Parallel()

	got := googleOIDCConfig(repo.OAuthPresetConfig{Enabled: true, ClientID: "cid", ClientSecret: "sec"})
	if got.IssuerURI != "https://accounts.google.com" {
		t.Errorf("IssuerURI = %q, want Google's issuer", got.IssuerURI)
	}
	if got.ClientID != "cid" || got.ClientSecret != "sec" {
		t.Errorf("credentials not carried over: %+v", got)
	}
	if !got.Enabled {
		t.Error("Enabled not carried over")
	}
}

// ---------------------------------------------------------------------------
// Dispatch — written before the registry refactor so it guards it
// ---------------------------------------------------------------------------

// fakeOIDCSettings serves provider config without a database.
type fakeOIDCSettings struct {
	google    repo.OAuthPresetConfig
	github    repo.OAuthPresetConfig
	generic   repo.GenericOIDCConfig
	force     bool
	provision repo.OIDCAutoProvisionDetails
}

func (f *fakeOIDCSettings) GetGoogle(context.Context) (repo.OAuthPresetConfig, error) {
	return f.google, nil
}
func (f *fakeOIDCSettings) GetGitHub(context.Context) (repo.OAuthPresetConfig, error) {
	return f.github, nil
}
func (f *fakeOIDCSettings) GetGenericOIDC(context.Context) (repo.GenericOIDCConfig, error) {
	return f.generic, nil
}
func (f *fakeOIDCSettings) GetBool(context.Context, string) (bool, error) { return f.force, nil }

// GetOIDCAutoProvision is the Provisioner's slice of the same settings
// row; the login arm of Exchange reads it through the Provisioner the
// service builds.
func (f *fakeOIDCSettings) GetOIDCAutoProvision(context.Context) (repo.OIDCAutoProvisionDetails, error) {
	return f.provision, nil
}

// oidcForTest builds a service with fake settings. GitHub is the only
// provider whose authorize URL is hand-built rather than derived from a
// discovery document, so it is the one dispatch path testable without a
// live IdP.
func oidcForTest(t *testing.T, settings *fakeOIDCSettings) *OIDCService {
	t.Helper()
	svc := &OIDCService{
		appURL:   "https://books.example",
		settings: settings,
		states:   newStateStore(),
	}
	// Mirror NewOIDCService: without the registry every dispatch returns
	// ErrOIDCUnknownProvider, which is a confusing way to fail a test.
	svc.providers = svc.newProviderRegistry()
	return svc
}

func TestAuthURLDispatchesToGitHub(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{
		github: repo.OAuthPresetConfig{Enabled: true, ClientID: "gh-client", ClientSecret: "s"},
	})

	raw, err := svc.AuthURL(context.Background(), repo.ProviderSlugGitHub, "https://books.example")
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("authorize URL does not parse: %v", err)
	}
	if u.Host != "github.com" || u.Path != "/login/oauth/authorize" {
		t.Errorf("dispatched to %s%s, want github.com/login/oauth/authorize", u.Host, u.Path)
	}

	q := u.Query()
	if q.Get("client_id") != "gh-client" {
		t.Errorf("client_id = %q, want gh-client", q.Get("client_id"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("no PKCE challenge on the authorize URL")
	}
	if q.Get("state") == "" {
		t.Fatal("no state on the authorize URL")
	}

	// The state must be resolvable, and must carry the routing facts the
	// callback needs — that link is the whole point of the store.
	entry, ok := svc.states.take(q.Get("state"))
	if !ok {
		t.Fatal("the minted state is not in the store — the callback would reject it")
	}
	if entry.ProviderSlug != repo.ProviderSlugGitHub {
		t.Errorf("state slug = %q, want github — the callback would dispatch wrongly", entry.ProviderSlug)
	}
	if entry.RedirectURL != q.Get("redirect_uri") {
		t.Errorf("stored redirect %q != sent redirect %q; the token exchange would be rejected",
			entry.RedirectURL, q.Get("redirect_uri"))
	}
	if entry.Intent != IntentLogin {
		t.Errorf("intent = %q, want login", entry.Intent)
	}
}

// The link flow reuses the same dispatch but must stamp a different
// intent and the initiating user, or the callback would log someone in
// instead of attaching an identity.
func TestAuthURLForLinkCarriesIntentAndUser(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{
		github: repo.OAuthPresetConfig{Enabled: true, ClientID: "gh-client", ClientSecret: "s"},
	})

	raw, err := svc.AuthURLForLink(context.Background(), repo.ProviderSlugGitHub, "https://books.example", "user-42")
	if err != nil {
		t.Fatalf("AuthURLForLink: %v", err)
	}
	u, _ := url.Parse(raw)

	entry, ok := svc.states.take(u.Query().Get("state"))
	if !ok {
		t.Fatal("state not stored")
	}
	if entry.Intent != IntentLink {
		t.Errorf("intent = %q, want link", entry.Intent)
	}
	if entry.LinkUserID != "user-42" {
		t.Errorf("LinkUserID = %q, want user-42", entry.LinkUserID)
	}
}

func TestAuthURLRejectsUnknownSlug(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{})
	_, err := svc.AuthURL(context.Background(), "myspace", "https://books.example")
	if !errors.Is(err, ErrOIDCUnknownProvider) {
		t.Errorf("err = %v, want ErrOIDCUnknownProvider", err)
	}
}

// A provider that is configured but switched off must not mint a URL.
func TestAuthURLRefusesDisabledProvider(t *testing.T) {
	t.Parallel()

	svc := oidcForTest(t, &fakeOIDCSettings{
		github: repo.OAuthPresetConfig{Enabled: false, ClientID: "gh", ClientSecret: "s"},
	})
	_, err := svc.AuthURL(context.Background(), repo.ProviderSlugGitHub, "https://books.example")
	if !errors.Is(err, ErrOIDCDisabled) {
		t.Errorf("err = %v, want ErrOIDCDisabled", err)
	}
}

// ---------------------------------------------------------------------------
// Diagnostics — the admin panel's "test this connection"
// ---------------------------------------------------------------------------
//
// The diagnostic deliberately fetches the discovery document itself
// rather than going through getDiscovery, so it can be driven against a
// stub issuer with no cache and no live IdP.

// A well-formed issuer passes every check and reports success.
func TestDiagnosticsPassAgainstAConformingIssuer(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           srv.URL,
			"authorization_endpoint":           srv.URL + "/authorize",
			"token_endpoint":                   srv.URL + "/token",
			"jwks_uri":                         srv.URL + "/jwks",
			"scopes_supported":                 []string{"openid", "profile", "email"},
			"response_types_supported":         []string{"code"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"k1"}]}`))
	})

	res := testGeneric(context.Background(), repo.GenericOIDCConfig{IssuerURI: srv.URL, ClientID: "cid"})

	if !res.Success {
		t.Fatalf("a conforming issuer failed: %+v", res.Checks)
	}
	want := map[string]CheckStatus{
		"Discovery":              CheckPass,
		"authorization_endpoint": CheckPass,
		"token_endpoint":         CheckPass,
		"jwks_uri":               CheckPass,
		"scope: openid":          CheckPass,
		"response_type: code":    CheckPass,
		"PKCE S256":              CheckPass,
		"JWKS fetch":             CheckPass,
	}
	got := map[string]CheckStatus{}
	for _, c := range res.Checks {
		got[c.Name] = c.Status
	}
	for name, status := range want {
		if got[name] != status {
			t.Errorf("check %q = %q, want %q", name, got[name], status)
		}
	}
}

// An issuer that advertises neither the code flow nor the email scope is
// a real misconfiguration: the missing response type fails the run, the
// missing optional scope only warns.
func TestDiagnosticsGradeMissingCapabilities(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint":   "https://idp.test/authorize",
			"token_endpoint":           "https://idp.test/token",
			"scopes_supported":         []string{"openid"},
			"response_types_supported": []string{"token"},
		})
	}))
	t.Cleanup(srv.Close)

	res := testGeneric(context.Background(), repo.GenericOIDCConfig{IssuerURI: srv.URL, ClientID: "cid"})

	if res.Success {
		t.Fatal("an issuer without the authorization-code flow was reported as usable")
	}
	got := map[string]CheckStatus{}
	for _, c := range res.Checks {
		got[c.Name] = c.Status
	}
	if got["response_type: code"] != CheckFail {
		t.Errorf("response_type: code = %q, want FAIL", got["response_type: code"])
	}
	if got["scope: email"] != CheckWarn {
		t.Errorf("scope: email = %q, want WARN — an unadvertised optional scope is not fatal", got["scope: email"])
	}
	if got["jwks_uri"] != CheckFail {
		t.Errorf("jwks_uri = %q, want FAIL", got["jwks_uri"])
	}
}

// An unreachable issuer reports the fetch failure rather than hanging or
// panicking on a nil response.
func TestDiagnosticsReportUnreachableIssuer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	res := testGeneric(context.Background(), repo.GenericOIDCConfig{IssuerURI: url, ClientID: "cid"})

	if res.Success || len(res.Checks) != 1 || res.Checks[0].Status != CheckFail {
		t.Fatalf("checks = %+v, want a single FAIL on discovery", res.Checks)
	}
}

// The two empty-input guards short-circuit before any network call, so a
// half-filled settings form gives an instant answer.
func TestDiagnosticsRefuseEmptyConfig(t *testing.T) {
	t.Parallel()

	if res := testGeneric(context.Background(), repo.GenericOIDCConfig{ClientID: "cid"}); res.Success ||
		len(res.Checks) != 1 || res.Checks[0].Name != "Issuer URI" {
		t.Errorf("empty issuer: checks = %+v, want a single Issuer URI failure", res.Checks)
	}
	if res := testGeneric(context.Background(), repo.GenericOIDCConfig{IssuerURI: "https://idp.test"}); res.Success ||
		len(res.Checks) != 1 || res.Checks[0].Name != "Client ID" {
		t.Errorf("empty client id: checks = %+v, want a single Client ID failure", res.Checks)
	}
	// GitHub has no discovery document, so its guard is its own.
	if res := testGitHub(context.Background(), repo.OAuthPresetConfig{}); res.Success ||
		len(res.Checks) != 1 || res.Checks[0].Name != "Client ID" {
		t.Errorf("github: checks = %+v, want a single Client ID failure", res.Checks)
	}
}
