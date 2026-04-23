package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

var (
	ErrOIDCNotConfigured     = errors.New("OIDC is not configured")
	ErrOIDCDisabled          = errors.New("OIDC is disabled")
	ErrOIDCStateMismatch     = errors.New("OIDC state mismatch")
	ErrOIDCLoginNotAllowed   = errors.New("this OIDC identity is not allowed to log in")
	ErrOIDCForceOnlyBlocked  = errors.New("OIDC-only mode cannot be enabled without a valid provider configuration")
)

// OIDC discovery + provider cache TTL. Matches the spec section 6.4 value
// so admins who edit settings in the UI don't wait an hour to see the new
// provider take effect — the handler also invalidates on save.
const providerCacheTTL = 1 * time.Hour

// stateTTL is the window in which a /login must complete the round-trip
// back to /callback. Matches the spec's 5-minute guidance.
const stateTTL = 5 * time.Minute

// OIDCService handles the OpenID Connect authorization code + PKCE flow,
// reading its settings from app_settings at runtime so admins can edit
// them without restarting the process.
type OIDCService struct {
	appURL    string
	settings  *repo.AppSettingsRepo
	users     *repo.UserRepo
	sessions  *repo.SessionRepo

	// cached discovery + oauth config, keyed by a fingerprint of the
	// provider settings so mutations invalidate automatically.
	mu           sync.Mutex
	cached       *cachedProvider
	cachedExpiry time.Time

	states *stateStore
}

type cachedProvider struct {
	fingerprint string
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauth       oauth2.Config
	claims      repo.ClaimMapping
	issuer      string
}

func NewOIDCService(settings *repo.AppSettingsRepo, users *repo.UserRepo, sessions *repo.SessionRepo, appURL string) *OIDCService {
	return &OIDCService{
		appURL:   strings.TrimRight(appURL, "/"),
		settings: settings,
		users:    users,
		sessions: sessions,
		states:   newStateStore(),
	}
}

// PublicConfig is the subset shown on the unauthenticated login page.
// ClientSecret and every non-public setting is intentionally absent.
type PublicConfig struct {
	Enabled       bool   `json:"enabled"`
	ProviderName  string `json:"providerName,omitempty"`
	ForceOnly     bool   `json:"forceOnly"`
	// Configured is true when OIDC has enough to at least attempt a
	// login — surfaced so the admin UI can explain why the enable
	// toggle is refused.
	Configured bool `json:"configured"`
}

// PublicConfig returns the settings the login page can read anonymously.
func (s *OIDCService) PublicConfig(ctx context.Context) (PublicConfig, error) {
	enabled, err := s.settings.GetBool(ctx, repo.SettingOIDCEnabled)
	if err != nil {
		return PublicConfig{}, err
	}
	provider, err := s.settings.GetOIDCProvider(ctx)
	if err != nil {
		return PublicConfig{}, err
	}
	force, err := s.settings.GetBool(ctx, repo.SettingOIDCForceOnlyMode)
	if err != nil {
		return PublicConfig{}, err
	}
	return PublicConfig{
		Enabled:      enabled && provider.ClientID != "" && provider.IssuerURI != "",
		ProviderName: provider.ProviderName,
		ForceOnly:    force && enabled,
		Configured:   provider.ClientID != "" && provider.IssuerURI != "",
	}, nil
}

// Enabled reports whether the OIDC login flow should be accepted.
func (s *OIDCService) Enabled(ctx context.Context) (bool, error) {
	on, err := s.settings.GetBool(ctx, repo.SettingOIDCEnabled)
	if err != nil {
		return false, err
	}
	if !on {
		return false, nil
	}
	p, err := s.settings.GetOIDCProvider(ctx)
	if err != nil {
		return false, err
	}
	return p.ClientID != "" && p.IssuerURI != "", nil
}

// AuthURL builds the provider authorization URL and returns the state the
// caller must set on the browser so the callback can resolve it. PKCE
// code_verifier is kept server-side, bound to the state.
func (s *OIDCService) AuthURL(ctx context.Context) (authURL, state string, err error) {
	if on, err := s.Enabled(ctx); err != nil {
		return "", "", err
	} else if !on {
		return "", "", ErrOIDCDisabled
	}

	cp, err := s.getProvider(ctx)
	if err != nil {
		return "", "", err
	}

	state, err = randomString(32)
	if err != nil {
		return "", "", err
	}
	nonce, err := randomString(32)
	if err != nil {
		return "", "", err
	}
	verifier, err := randomString(64)
	if err != nil {
		return "", "", err
	}
	challenge := pkceChallengeS256(verifier)

	s.states.put(state, stateEntry{
		Nonce:        nonce,
		CodeVerifier: verifier,
		CreatedAt:    time.Now(),
	})

	u := cp.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return u, state, nil
}

// Exchange trades the authorization code for tokens, verifies the ID
// token, finds or provisions the matching local user, and issues a
// BookLore session. Returns ErrOIDCStateMismatch when the state token is
// missing/expired/reused.
func (s *OIDCService) Exchange(ctx context.Context, code, state, userAgent string) (model.Session, model.User, error) {
	entry, ok := s.states.take(state)
	if !ok {
		return model.Session{}, model.User{}, ErrOIDCStateMismatch
	}

	if on, err := s.Enabled(ctx); err != nil {
		return model.Session{}, model.User{}, err
	} else if !on {
		return model.Session{}, model.User{}, ErrOIDCDisabled
	}

	cp, err := s.getProvider(ctx)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	token, err := cp.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", entry.CodeVerifier),
	)
	if err != nil {
		return model.Session{}, model.User{}, fmt.Errorf("token exchange: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return model.Session{}, model.User{}, errors.New("provider response missing id_token")
	}
	idToken, err := cp.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return model.Session{}, model.User{}, fmt.Errorf("id_token verify: %w", err)
	}
	if idToken.Nonce != entry.Nonce {
		return model.Session{}, model.User{}, errors.New("nonce mismatch")
	}

	claims, err := extractClaims(ctx, cp, token, idToken)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	provision, err := s.settings.GetOIDCAutoProvision(ctx)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	u, err := s.findOrProvisionUser(ctx, cp.issuer, claims, provision)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	// Keep display-name / avatar fresh on every login.
	_ = s.users.SyncOIDCProfile(ctx, u.ID, claims.Name, claims.Picture)

	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())
	return sess, u, nil
}

// Invalidate drops the cached *oidc.Provider so the next AuthURL call
// re-runs discovery. Handlers call this after saving provider settings.
func (s *OIDCService) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.cachedExpiry = time.Time{}
	s.mu.Unlock()
}

// ValidateForceOnlyTransition enforces the server-side guard described in
// spec section 4.4: an admin cannot enable force-only mode without a
// usable provider configuration, else they lock themselves out.
func (s *OIDCService) ValidateForceOnlyTransition(ctx context.Context, next bool) error {
	if !next {
		return nil
	}
	if on, err := s.Enabled(ctx); err != nil {
		return err
	} else if !on {
		return ErrOIDCForceOnlyBlocked
	}
	return nil
}

// getProvider returns a cached discovery result, re-running discovery
// when the settings change or the TTL expires.
func (s *OIDCService) getProvider(ctx context.Context) (*cachedProvider, error) {
	details, err := s.settings.GetOIDCProvider(ctx)
	if err != nil {
		return nil, err
	}
	if details.ClientID == "" || details.IssuerURI == "" {
		return nil, ErrOIDCNotConfigured
	}
	fp := fingerprint(details, s.redirectURL())

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && s.cached.fingerprint == fp && time.Now().Before(s.cachedExpiry) {
		return s.cached, nil
	}

	provider, err := oidc.NewProvider(ctx, details.IssuerURI)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := splitScopes(details.Scopes)
	oauthCfg := oauth2.Config{
		ClientID:     details.ClientID,
		ClientSecret: details.ClientSecret,
		RedirectURL:  s.redirectURL(),
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}
	cp := &cachedProvider{
		fingerprint: fp,
		provider:    provider,
		verifier:    provider.Verifier(&oidc.Config{ClientID: details.ClientID}),
		oauth:       oauthCfg,
		claims:      details.ClaimMapping,
		issuer:      details.IssuerURI,
	}
	s.cached = cp
	s.cachedExpiry = time.Now().Add(providerCacheTTL)
	return cp, nil
}

func (s *OIDCService) redirectURL() string {
	if s.appURL == "" {
		return ""
	}
	return s.appURL + "/api/v1/auth/oidc/callback"
}

// findOrProvisionUser implements section 6.1's lookup cascade adapted to
// this codebase's admin/user role model.
func (s *OIDCService) findOrProvisionUser(ctx context.Context, issuer string, claims resolvedClaims, provision repo.OIDCAutoProvisionDetails) (model.User, error) {
	// 1) Match by OIDC identity.
	u, err := s.users.GetByOIDC(ctx, issuer, claims.Subject)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return model.User{}, err
	}
	if err == nil {
		return u, nil
	}

	// 2) Match by email and link, if linking is allowed.
	if claims.Email != "" {
		existing, err := s.users.GetByEmail(ctx, claims.Email)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return model.User{}, err
		}
		if err == nil {
			if !provision.AllowLocalAccountLinking && existing.OIDCSubject == nil {
				return model.User{}, ErrOIDCLoginNotAllowed
			}
			if err := s.users.LinkOIDC(ctx, existing.ID, issuer, claims.Subject); err != nil {
				return model.User{}, err
			}
			return existing, nil
		}
	}

	// 3) Auto-provision — refused outright when the flag is off.
	if !provision.EnableAutoProvisioning {
		// The very first OIDC user on an empty instance still boots so
		// an operator can recover from a clean DB. Matches the existing
		// behaviour before this refactor.
		n, err := s.users.Count(ctx)
		if err != nil {
			return model.User{}, err
		}
		if n > 0 {
			return model.User{}, ErrOIDCLoginNotAllowed
		}
	}

	role := model.RoleUser
	if provision.DefaultRole == "admin" {
		role = model.RoleAdmin
	}
	// Bootstrap: the first user on an empty DB is always the admin so
	// the instance ends up with someone who can manage it.
	if n, err := s.users.Count(ctx); err == nil && n == 0 {
		role = model.RoleAdmin
	}

	email := claims.Email
	if email == "" {
		return model.User{}, errors.New("OIDC provider did not return an email claim and email is required")
	}
	return s.users.CreateOIDC(ctx, email, claims.Name, role, issuer, claims.Subject)
}

type resolvedClaims struct {
	Subject string
	Email   string
	Name    string
	Picture string
}

// extractClaims pulls values out of ID token + userinfo according to the
// admin-configured claim mapping, falling back to the standard names
// documented in spec section 6.1.
func extractClaims(ctx context.Context, cp *cachedProvider, token *oauth2.Token, idToken *oidc.IDToken) (resolvedClaims, error) {
	// Merge id_token claims + userinfo into one bag — userinfo wins when
	// both are present so a provider that only exposes email on the
	// userinfo endpoint (e.g. some AD FS setups) still works.
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return resolvedClaims{}, fmt.Errorf("id_token claims: %w", err)
	}
	if ui, err := cp.provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil {
		var uclaims map[string]any
		if err := ui.Claims(&uclaims); err == nil {
			for k, v := range uclaims {
				if _, ok := claims[k]; !ok {
					claims[k] = v
				}
			}
		}
	}

	mapping := cp.claims
	pick := func(key, fallback string) string {
		if v, ok := claims[key].(string); ok && v != "" {
			return v
		}
		if v, ok := claims[fallback].(string); ok && v != "" {
			return v
		}
		return ""
	}

	out := resolvedClaims{
		Subject: idToken.Subject,
		Email:   strings.ToLower(strings.TrimSpace(pick(mapping.Email, "email"))),
		Name:    pick(mapping.Name, "name"),
		Picture: pick("picture", "picture"),
	}
	if out.Name == "" {
		// Compose from given_name + family_name the same way most
		// providers do when the single `name` claim is missing.
		g, _ := claims["given_name"].(string)
		f, _ := claims["family_name"].(string)
		out.Name = strings.TrimSpace(g + " " + f)
	}
	return out, nil
}

// TestConnection runs the checks in section 6.7 against a prospective
// provider configuration. Uses a fresh HTTP client — we deliberately
// don't consult the service cache so admins see current reality.
func (s *OIDCService) TestConnection(ctx context.Context, p repo.OIDCProviderDetails) TestResult {
	out := TestResult{}
	if strings.TrimSpace(p.IssuerURI) == "" {
		out.add("Issuer URI", CheckFail, "issuer URI is empty")
		return out
	}
	if strings.TrimSpace(p.ClientID) == "" {
		out.add("Client ID", CheckFail, "client id is empty")
		return out
	}

	cli := &http.Client{Timeout: 10 * time.Second}
	discoveryURL := strings.TrimRight(p.IssuerURI, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		out.add("Discovery", CheckFail, err.Error())
		return out
	}
	resp, err := cli.Do(req)
	if err != nil {
		out.add("Discovery", CheckFail, fmt.Sprintf("fetch %s: %v", discoveryURL, err))
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out.add("Discovery", CheckFail, fmt.Sprintf("%s returned %d", discoveryURL, resp.StatusCode))
		return out
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		out.add("Discovery", CheckFail, err.Error())
		return out
	}
	out.add("Discovery", CheckPass, "fetched openid-configuration")

	var doc discoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		out.add("Discovery parse", CheckFail, err.Error())
		return out
	}

	if doc.AuthorizationEndpoint == "" {
		out.add("authorization_endpoint", CheckFail, "missing")
	} else {
		out.add("authorization_endpoint", CheckPass, doc.AuthorizationEndpoint)
	}
	if doc.TokenEndpoint == "" {
		out.add("token_endpoint", CheckFail, "missing")
	} else {
		out.add("token_endpoint", CheckPass, doc.TokenEndpoint)
	}
	if doc.JWKSURI == "" {
		out.add("jwks_uri", CheckFail, "missing")
	} else {
		out.add("jwks_uri", CheckPass, doc.JWKSURI)
	}

	// Scopes: issuer MUST advertise openid; profile/email warn when
	// missing because the admin's claim mapping almost certainly needs
	// them.
	has := map[string]bool{}
	for _, sc := range doc.ScopesSupported {
		has[sc] = true
	}
	for _, required := range []string{"openid", "profile", "email"} {
		if has[required] {
			out.add("scope: "+required, CheckPass, "advertised")
		} else if required == "openid" {
			out.add("scope: openid", CheckFail, "issuer does not advertise openid")
		} else {
			out.add("scope: "+required, CheckWarn, "not advertised — claim mapping may fail")
		}
	}

	// response_type=code
	hasCode := false
	for _, rt := range doc.ResponseTypesSupported {
		if rt == "code" {
			hasCode = true
			break
		}
	}
	if hasCode {
		out.add("response_type: code", CheckPass, "supported")
	} else {
		out.add("response_type: code", CheckFail, "authorization code flow not supported")
	}

	// PKCE S256
	hasS256 := false
	for _, m := range doc.CodeChallengeMethodsSupported {
		if m == "S256" {
			hasS256 = true
			break
		}
	}
	if hasS256 {
		out.add("PKCE S256", CheckPass, "supported")
	} else {
		out.add("PKCE S256", CheckWarn, "not advertised — BookLore sends S256 anyway")
	}

	// JWKS fetch
	if doc.JWKSURI != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, doc.JWKSURI, nil)
		jresp, err := cli.Do(req)
		if err != nil {
			out.add("JWKS fetch", CheckFail, err.Error())
		} else {
			defer jresp.Body.Close()
			if jresp.StatusCode != 200 {
				out.add("JWKS fetch", CheckFail, fmt.Sprintf("%s returned %d", doc.JWKSURI, jresp.StatusCode))
			} else {
				var keys struct {
					Keys []json.RawMessage `json:"keys"`
				}
				_ = json.NewDecoder(jresp.Body).Decode(&keys)
				if len(keys.Keys) == 0 {
					out.add("JWKS fetch", CheckWarn, "JWKS has no keys")
				} else {
					out.add("JWKS fetch", CheckPass, fmt.Sprintf("%d keys", len(keys.Keys)))
				}
			}
		}
	}

	out.Success = true
	for _, c := range out.Checks {
		if c.Status == CheckFail {
			out.Success = false
			break
		}
	}
	return out
}

type discoveryDoc struct {
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	JWKSURI                       string   `json:"jwks_uri"`
	ScopesSupported               []string `json:"scopes_supported"`
	ResponseTypesSupported        []string `json:"response_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// CheckStatus + TestCheck mirror the spec's diagnostic DTO so the UI can
// render a clear PASS/FAIL/WARN table.
type CheckStatus string

const (
	CheckPass CheckStatus = "PASS"
	CheckFail CheckStatus = "FAIL"
	CheckWarn CheckStatus = "WARN"
)

type TestCheck struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

type TestResult struct {
	Success bool        `json:"success"`
	Checks  []TestCheck `json:"checks"`
}

func (t *TestResult) add(name string, status CheckStatus, msg string) {
	t.Checks = append(t.Checks, TestCheck{Name: name, Status: status, Message: msg})
}

// -------------- helpers --------------

type stateEntry struct {
	Nonce        string
	CodeVerifier string
	CreatedAt    time.Time
}

// stateStore is a tiny in-memory single-use TTL map — we don't pull in a
// cache library for one use, and the state set is always small.
type stateStore struct {
	mu sync.Mutex
	m  map[string]stateEntry
}

func newStateStore() *stateStore { return &stateStore{m: map[string]stateEntry{}} }

func (s *stateStore) put(state string, e stateEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reap()
	s.m[state] = e
}

func (s *stateStore) take(state string) (stateEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reap()
	e, ok := s.m[state]
	if !ok {
		return stateEntry{}, false
	}
	delete(s.m, state)
	return e, true
}

func (s *stateStore) reap() {
	cutoff := time.Now().Add(-stateTTL)
	for k, v := range s.m {
		if v.CreatedAt.Before(cutoff) {
			delete(s.m, k)
		}
	}
}

func randomString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func splitScopes(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return []string{oidc.ScopeOpenID, "profile", "email"}
	}
	// openid is mandatory.
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	if _, ok := seen[oidc.ScopeOpenID]; !ok {
		out = append([]string{oidc.ScopeOpenID}, out...)
	}
	return out
}

// fingerprint returns a stable key for the cache so edits to any
// meaningful field bust the cached provider. We don't hash — plain
// concat is fine for an in-process cache.
func fingerprint(p repo.OIDCProviderDetails, redirectURL string) string {
	return strings.Join([]string{
		p.IssuerURI, p.ClientID, p.ClientSecret, p.Scopes, redirectURL,
	}, "|")
}

