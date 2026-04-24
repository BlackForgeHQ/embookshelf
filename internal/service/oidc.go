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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

var (
	ErrOIDCNotConfigured    = errors.New("OIDC is not configured")
	ErrOIDCDisabled         = errors.New("OIDC is disabled")
	ErrOIDCStateMismatch    = errors.New("OIDC state mismatch")
	ErrOIDCLoginNotAllowed  = errors.New("this OIDC identity is not allowed to log in")
	ErrOIDCForceOnlyBlocked = errors.New("OIDC-only mode cannot be enabled without at least one configured provider")
	ErrOIDCUnknownProvider  = errors.New("unknown OIDC provider")
)

const (
	// providerCacheTTL is how long the discovery result for a generic
	// OIDC issuer is kept before re-fetching. Admins saving settings
	// also call Invalidate to bust it explicitly.
	providerCacheTTL = 1 * time.Hour

	// stateTTL is the window in which a /login must complete the
	// round-trip back to /callback. Matches the spec's 5-min guidance.
	stateTTL = 5 * time.Minute
)

// OIDCService is the multi-provider OIDC/OAuth login service.
// Google, GitHub, and a custom OIDC provider each have their own
// settings row and can be enabled in parallel.
type OIDCService struct {
	appURL   string
	settings *repo.AppSettingsRepo
	users    *repo.UserRepo
	sessions *repo.SessionRepo

	states *stateStore

	// Discovery cache for the generic OIDC provider only. Google runs
	// through the same path but its issuer is fixed so we'd still hit
	// discovery once per restart — harmless.
	discoveryMu  sync.Mutex
	discoveryKey string
	discoveryVal *cachedDiscovery
	discoveryExp time.Time
}

type cachedDiscovery struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
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

// -----------------------------------------------------------------------------
// Provider registry / public API
// -----------------------------------------------------------------------------

// PublicProvider is one enabled login option surfaced on the public
// login page — enough to render "Sign in with X" without leaking
// secrets.
type PublicProvider struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "oidc" | "google" | "github"
	LoginURL string `json:"loginUrl"`
}

// PublicConfig is what the login page reads anonymously.
type PublicConfig struct {
	Providers []PublicProvider `json:"providers"`
	ForceOnly bool             `json:"forceOnly"`
}

// PublicConfig builds the anonymous login page view.
func (s *OIDCService) PublicConfig(ctx context.Context) (PublicConfig, error) {
	force, err := s.settings.GetBool(ctx, repo.SettingOIDCForceOnlyMode)
	if err != nil {
		return PublicConfig{}, err
	}
	providers, err := s.publicProviders(ctx)
	if err != nil {
		return PublicConfig{}, err
	}
	return PublicConfig{
		Providers: providers,
		ForceOnly: force && len(providers) > 0,
	}, nil
}

// Enabled reports whether at least one provider is enabled and usable.
func (s *OIDCService) Enabled(ctx context.Context) (bool, error) {
	ps, err := s.publicProviders(ctx)
	if err != nil {
		return false, err
	}
	return len(ps) > 0, nil
}

func (s *OIDCService) publicProviders(ctx context.Context) ([]PublicProvider, error) {
	var out []PublicProvider
	if g, err := s.settings.GetGoogle(ctx); err != nil {
		return nil, err
	} else if googleUsable(g) {
		out = append(out, PublicProvider{
			Slug: repo.ProviderSlugGoogle, Name: "Google", Kind: "google",
			LoginURL: "/api/v1/auth/oidc/" + repo.ProviderSlugGoogle,
		})
	}
	if g, err := s.settings.GetGitHub(ctx); err != nil {
		return nil, err
	} else if githubUsable(g) {
		out = append(out, PublicProvider{
			Slug: repo.ProviderSlugGitHub, Name: "GitHub", Kind: "github",
			LoginURL: "/api/v1/auth/oidc/" + repo.ProviderSlugGitHub,
		})
	}
	if g, err := s.settings.GetGenericOIDC(ctx); err != nil {
		return nil, err
	} else if genericUsable(g) {
		name := g.ProviderName
		if name == "" {
			name = "SSO"
		}
		out = append(out, PublicProvider{
			Slug: repo.ProviderSlugGeneric, Name: name, Kind: "oidc",
			LoginURL: "/api/v1/auth/oidc/" + repo.ProviderSlugGeneric,
		})
	}
	return out, nil
}

func googleUsable(c repo.OAuthPresetConfig) bool {
	return c.Enabled && c.ClientID != "" && c.ClientSecret != ""
}
func githubUsable(c repo.OAuthPresetConfig) bool {
	return c.Enabled && c.ClientID != "" && c.ClientSecret != ""
}
func genericUsable(c repo.GenericOIDCConfig) bool {
	return c.Enabled && c.ClientID != "" && c.IssuerURI != ""
}

// ValidateForceOnlyTransition refuses to enable force-only mode when no
// provider is enabled — admins would otherwise lock themselves out.
func (s *OIDCService) ValidateForceOnlyTransition(ctx context.Context, next bool) error {
	if !next {
		return nil
	}
	ps, err := s.publicProviders(ctx)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return ErrOIDCForceOnlyBlocked
	}
	return nil
}

// Invalidate drops the generic-OIDC discovery cache. Settings handlers
// call this after saving so admins don't have to wait for a TTL.
func (s *OIDCService) Invalidate() {
	s.discoveryMu.Lock()
	s.discoveryKey = ""
	s.discoveryVal = nil
	s.discoveryExp = time.Time{}
	s.discoveryMu.Unlock()
}

// -----------------------------------------------------------------------------
// Flow entrypoints
// -----------------------------------------------------------------------------

// AuthURL builds the authorization URL for the given provider slug.
// The state is held server-side and carries the slug + redirect URL so
// the callback can route back to the right provider and rebuild the
// oauth config with a matching redirect_uri.
//
// baseURL is the public origin to reach this server ("https://host[:port]");
// when APP_URL is configured the handler passes that, otherwise it falls
// back to the current request's scheme+host so local dev works with no
// extra config.
func (s *OIDCService) AuthURL(ctx context.Context, slug, baseURL string) (string, error) {
	redirect := s.resolveRedirectURL(baseURL)
	if redirect == "" {
		return "", ErrOIDCNotConfigured
	}
	switch slug {
	case repo.ProviderSlugGoogle:
		return s.authURLGoogle(ctx, redirect)
	case repo.ProviderSlugGitHub:
		return s.authURLGitHub(ctx, redirect)
	case repo.ProviderSlugGeneric:
		return s.authURLGeneric(ctx, redirect)
	default:
		return "", ErrOIDCUnknownProvider
	}
}

// Exchange completes the callback by inspecting the state (which
// carries the provider slug + original redirect URI), dispatching to
// the right backend, and issuing a BookLore session.
func (s *OIDCService) Exchange(ctx context.Context, code, state, userAgent string) (model.Session, model.User, error) {
	entry, ok := s.states.take(state)
	if !ok {
		return model.Session{}, model.User{}, ErrOIDCStateMismatch
	}

	provision, err := s.settings.GetOIDCAutoProvision(ctx)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	redirect := entry.RedirectURL
	if redirect == "" {
		redirect = s.resolveRedirectURL("")
	}

	var (
		claims resolvedClaims
		issuer string
	)
	switch entry.ProviderSlug {
	case repo.ProviderSlugGoogle:
		cfg, err := s.settings.GetGoogle(ctx)
		if err != nil {
			return model.Session{}, model.User{}, err
		}
		if !googleUsable(cfg) {
			return model.Session{}, model.User{}, ErrOIDCDisabled
		}
		claims, issuer, err = s.oidcCallback(ctx, code, entry, googleOIDCConfig(cfg), redirect)
		if err != nil {
			return model.Session{}, model.User{}, err
		}
	case repo.ProviderSlugGitHub:
		cfg, err := s.settings.GetGitHub(ctx)
		if err != nil {
			return model.Session{}, model.User{}, err
		}
		if !githubUsable(cfg) {
			return model.Session{}, model.User{}, ErrOIDCDisabled
		}
		claims, err = s.githubCallback(ctx, code, entry, cfg, redirect)
		if err != nil {
			return model.Session{}, model.User{}, err
		}
		issuer = "https://github.com"
	case repo.ProviderSlugGeneric:
		cfg, err := s.settings.GetGenericOIDC(ctx)
		if err != nil {
			return model.Session{}, model.User{}, err
		}
		if !genericUsable(cfg) {
			return model.Session{}, model.User{}, ErrOIDCDisabled
		}
		claims, issuer, err = s.oidcCallback(ctx, code, entry, cfg, redirect)
		if err != nil {
			return model.Session{}, model.User{}, err
		}
	default:
		return model.Session{}, model.User{}, ErrOIDCUnknownProvider
	}

	u, err := s.findOrProvisionUser(ctx, issuer, claims, provision)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	_ = s.users.SyncOIDCProfile(ctx, u.ID, claims.Name, claims.Picture)

	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())
	return sess, u, nil
}

// -----------------------------------------------------------------------------
// AuthURL builders
// -----------------------------------------------------------------------------

func (s *OIDCService) authURLGoogle(ctx context.Context, redirect string) (string, error) {
	cfg, err := s.settings.GetGoogle(ctx)
	if err != nil {
		return "", err
	}
	if !googleUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return s.authURLOIDC(ctx, repo.ProviderSlugGoogle, googleOIDCConfig(cfg), redirect)
}

func (s *OIDCService) authURLGeneric(ctx context.Context, redirect string) (string, error) {
	cfg, err := s.settings.GetGenericOIDC(ctx)
	if err != nil {
		return "", err
	}
	if !genericUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return s.authURLOIDC(ctx, repo.ProviderSlugGeneric, cfg, redirect)
}

func (s *OIDCService) authURLOIDC(ctx context.Context, slug string, cfg repo.GenericOIDCConfig, redirect string) (string, error) {
	disc, err := s.getDiscovery(ctx, cfg)
	if err != nil {
		return "", err
	}
	state, nonce, verifier, err := s.issueState(slug, redirect)
	if err != nil {
		return "", err
	}
	oauthCfg := oidcOAuthConfig(cfg, disc.provider, redirect)
	challenge := pkceChallengeS256(verifier)
	u := oauthCfg.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return u, nil
}

// authURLGitHub builds the GitHub authorize URL by hand; GitHub is not
// an OIDC provider so there's no discovery document.
func (s *OIDCService) authURLGitHub(ctx context.Context, redirect string) (string, error) {
	cfg, err := s.settings.GetGitHub(ctx)
	if err != nil {
		return "", err
	}
	if !githubUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	state, _, verifier, err := s.issueState(repo.ProviderSlugGitHub, redirect)
	if err != nil {
		return "", err
	}
	v := url.Values{}
	v.Set("client_id", cfg.ClientID)
	v.Set("redirect_uri", redirect)
	v.Set("scope", "read:user user:email")
	v.Set("state", state)
	v.Set("code_challenge", pkceChallengeS256(verifier))
	v.Set("code_challenge_method", "S256")
	v.Set("allow_signup", "true")
	return "https://github.com/login/oauth/authorize?" + v.Encode(), nil
}

func (s *OIDCService) issueState(slug, redirect string) (state, nonce, verifier string, err error) {
	state, err = randomString(32)
	if err != nil {
		return "", "", "", err
	}
	nonce, err = randomString(32)
	if err != nil {
		return "", "", "", err
	}
	verifier, err = randomString(64)
	if err != nil {
		return "", "", "", err
	}
	s.states.put(state, stateEntry{
		Nonce:        nonce,
		CodeVerifier: verifier,
		CreatedAt:    time.Now(),
		ProviderSlug: slug,
		RedirectURL:  redirect,
	})
	return state, nonce, verifier, nil
}

// -----------------------------------------------------------------------------
// Callbacks
// -----------------------------------------------------------------------------

func (s *OIDCService) oidcCallback(ctx context.Context, code string, entry stateEntry, cfg repo.GenericOIDCConfig, redirect string) (resolvedClaims, string, error) {
	disc, err := s.getDiscovery(ctx, cfg)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	oauthCfg := oidcOAuthConfig(cfg, disc.provider, redirect)
	token, err := oauthCfg.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", entry.CodeVerifier),
	)
	if err != nil {
		return resolvedClaims{}, "", fmt.Errorf("token exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return resolvedClaims{}, "", errors.New("provider response missing id_token")
	}
	idToken, err := disc.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return resolvedClaims{}, "", fmt.Errorf("id_token verify: %w", err)
	}
	if idToken.Nonce != entry.Nonce {
		return resolvedClaims{}, "", errors.New("nonce mismatch")
	}
	claims, err := extractClaims(ctx, disc, token, idToken, cfg.ClaimMapping)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	return claims, cfg.IssuerURI, nil
}

// githubCallback runs the GitHub OAuth token exchange + REST user
// lookup. No ID token; the GitHub user id is the stable subject.
func (s *OIDCService) githubCallback(ctx context.Context, code string, entry stateEntry, cfg repo.OAuthPresetConfig, redirect string) (resolvedClaims, error) {
	body := url.Values{}
	body.Set("client_id", cfg.ClientID)
	body.Set("client_secret", cfg.ClientSecret)
	body.Set("code", code)
	body.Set("redirect_uri", redirect)
	body.Set("code_verifier", entry.CodeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(body.Encode()))
	if err != nil {
		return resolvedClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient().Do(req)
	if err != nil {
		return resolvedClaims{}, fmt.Errorf("github token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return resolvedClaims{}, fmt.Errorf("github token exchange returned %d", resp.StatusCode)
	}
	var tokenResp struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return resolvedClaims{}, fmt.Errorf("github token decode: %w", err)
	}
	if tokenResp.Error != "" {
		return resolvedClaims{}, fmt.Errorf("github: %s: %s", tokenResp.Error, tokenResp.ErrorDescription)
	}
	if tokenResp.AccessToken == "" {
		return resolvedClaims{}, errors.New("github did not return an access_token")
	}

	user, err := githubFetchUser(ctx, "https://api.github.com/user", tokenResp.AccessToken)
	if err != nil {
		return resolvedClaims{}, err
	}
	email := strings.TrimSpace(user.Email)
	if email == "" {
		email, _ = githubFetchPrimaryEmail(ctx, "https://api.github.com/user/emails", tokenResp.AccessToken)
	}
	if email == "" {
		return resolvedClaims{}, errors.New("github account has no verified email accessible via user:email scope")
	}
	return resolvedClaims{
		Subject: strconv.FormatInt(user.ID, 10),
		Email:   strings.ToLower(email),
		Name:    orString(user.Name, user.Login),
		Picture: user.AvatarURL,
	}, nil
}

type githubUserResp struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

func githubFetchUser(ctx context.Context, userURL, accessToken string) (githubUserResp, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return githubUserResp{}, fmt.Errorf("github user fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return githubUserResp{}, fmt.Errorf("github user fetch: status %d", resp.StatusCode)
	}
	var u githubUserResp
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return githubUserResp{}, err
	}
	return u, nil
}

func githubFetchPrimaryEmail(ctx context.Context, emailsURL, accessToken string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, emailsURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github emails fetch: status %d", resp.StatusCode)
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", errors.New("no verified emails returned by github")
}

// -----------------------------------------------------------------------------
// Claim extraction (OIDC)
// -----------------------------------------------------------------------------

type resolvedClaims struct {
	Subject string
	Email   string
	Name    string
	Picture string
}

func extractClaims(ctx context.Context, disc *cachedDiscovery, token *oauth2.Token, idToken *oidc.IDToken, mapping repo.ClaimMapping) (resolvedClaims, error) {
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return resolvedClaims{}, fmt.Errorf("id_token claims: %w", err)
	}
	if ui, err := disc.provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil {
		var uclaims map[string]any
		if err := ui.Claims(&uclaims); err == nil {
			for k, v := range uclaims {
				if _, ok := claims[k]; !ok {
					claims[k] = v
				}
			}
		}
	}

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
		g, _ := claims["given_name"].(string)
		f, _ := claims["family_name"].(string)
		out.Name = strings.TrimSpace(g + " " + f)
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// User provisioning
// -----------------------------------------------------------------------------

func (s *OIDCService) findOrProvisionUser(ctx context.Context, issuer string, claims resolvedClaims, provision repo.OIDCAutoProvisionDetails) (model.User, error) {
	// 1) Match by OIDC identity.
	u, err := s.users.GetByOIDC(ctx, issuer, claims.Subject)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return model.User{}, err
	}
	if err == nil {
		return u, nil
	}

	// 2) Match by email and link.
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

	// 3) Auto-provision.
	if !provision.EnableAutoProvisioning {
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
	if n, err := s.users.Count(ctx); err == nil && n == 0 {
		role = model.RoleAdmin
	}

	if claims.Email == "" {
		return model.User{}, errors.New("OIDC provider did not return an email claim and email is required")
	}
	return s.users.CreateOIDC(ctx, claims.Email, claims.Name, role, issuer, claims.Subject)
}

// -----------------------------------------------------------------------------
// Discovery cache (generic OIDC + Google share this path)
// -----------------------------------------------------------------------------

func (s *OIDCService) getDiscovery(ctx context.Context, cfg repo.GenericOIDCConfig) (*cachedDiscovery, error) {
	if cfg.IssuerURI == "" || cfg.ClientID == "" {
		return nil, ErrOIDCNotConfigured
	}
	key := cfg.IssuerURI + "|" + cfg.ClientID

	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	if s.discoveryVal != nil && s.discoveryKey == key && time.Now().Before(s.discoveryExp) {
		return s.discoveryVal, nil
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURI)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	d := &cachedDiscovery{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}
	s.discoveryKey = key
	s.discoveryVal = d
	s.discoveryExp = time.Now().Add(providerCacheTTL)
	return d, nil
}

// oidcOAuthConfig builds a fresh oauth2.Config for a request. The
// redirect URL varies per-request (APP_URL fallback to request origin),
// so we don't cache it — only the provider discovery is cached.
func oidcOAuthConfig(cfg repo.GenericOIDCConfig, provider *oidc.Provider, redirect string) oauth2.Config {
	return oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  redirect,
		Endpoint:     provider.Endpoint(),
		Scopes:       splitScopes(cfg.Scopes),
	}
}

// googleOIDCConfig widens Google's tiny preset config into the generic
// OIDC shape the discovery path consumes. Issuer + scopes + claim
// mapping are all baked in — admins only supply credentials.
func googleOIDCConfig(c repo.OAuthPresetConfig) repo.GenericOIDCConfig {
	return repo.GenericOIDCConfig{
		Enabled:      c.Enabled,
		ProviderName: "Google",
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		IssuerURI:    "https://accounts.google.com",
		Scopes:       "openid profile email",
		ClaimMapping: repo.ClaimMapping{
			Username: "email",
			Email:    "email",
			Name:     "name",
		},
	}
}

// resolveRedirectURL returns the absolute callback URL.
//   - If APP_URL was configured at boot, that wins (production deploys
//     behind a reverse proxy set it explicitly so redirect_uri matches
//     what's registered with Google/GitHub/…).
//   - Otherwise the handler passes the current request's origin
//     ("scheme://host[:port]") so local dev and self-hosting work
//     without any extra config.
func (s *OIDCService) resolveRedirectURL(fallbackBase string) string {
	base := s.appURL
	if base == "" {
		base = strings.TrimRight(fallbackBase, "/")
	}
	if base == "" {
		return ""
	}
	return base + "/api/v1/auth/oidc/callback"
}

// -----------------------------------------------------------------------------
// Test Connection
// -----------------------------------------------------------------------------

// CheckStatus + TestCheck mirror the spec's diagnostic DTO.
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

// TestGeneric runs the discovery-based checks.
func (s *OIDCService) TestGeneric(ctx context.Context, cfg repo.GenericOIDCConfig) TestResult {
	return testOIDCIssuer(ctx, cfg.IssuerURI, cfg.ClientID)
}

// TestGoogle reuses the generic path after filling in Google's issuer.
func (s *OIDCService) TestGoogle(ctx context.Context, cfg repo.OAuthPresetConfig) TestResult {
	return testOIDCIssuer(ctx, "https://accounts.google.com", cfg.ClientID)
}

// TestGitHub pings the fixed GitHub endpoints (no discovery doc).
func (s *OIDCService) TestGitHub(ctx context.Context, cfg repo.OAuthPresetConfig) TestResult {
	out := TestResult{}
	if cfg.ClientID == "" {
		out.add("Client ID", CheckFail, "client id is empty")
		return out
	}
	cli := httpClient()
	for _, ep := range []struct {
		name, url string
	}{
		{"authorize endpoint", "https://github.com/login/oauth/authorize"},
		{"user API", "https://api.github.com/user"},
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ep.url, nil)
		resp, err := cli.Do(req)
		if err != nil {
			out.add(ep.name, CheckFail, err.Error())
			continue
		}
		_ = resp.Body.Close()
		out.add(ep.name, CheckPass, fmt.Sprintf("%s reachable (%d)", ep.url, resp.StatusCode))
	}
	if cfg.ClientSecret == "" {
		out.add("client secret", CheckFail, "GitHub OAuth apps require a client secret")
	} else {
		out.add("client secret", CheckPass, "set")
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

func testOIDCIssuer(ctx context.Context, issuer, clientID string) TestResult {
	out := TestResult{}
	if strings.TrimSpace(issuer) == "" {
		out.add("Issuer URI", CheckFail, "issuer URI is empty")
		return out
	}
	if strings.TrimSpace(clientID) == "" {
		out.add("Client ID", CheckFail, "client id is empty")
		return out
	}
	cli := httpClient()
	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
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
	defer func() { _ = resp.Body.Close() }()
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

	for _, p := range []struct {
		name, value string
	}{
		{"authorization_endpoint", doc.AuthorizationEndpoint},
		{"token_endpoint", doc.TokenEndpoint},
		{"jwks_uri", doc.JWKSURI},
	} {
		if p.value == "" {
			out.add(p.name, CheckFail, "missing")
		} else {
			out.add(p.name, CheckPass, p.value)
		}
	}

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

	codeOk := false
	for _, rt := range doc.ResponseTypesSupported {
		if rt == "code" {
			codeOk = true
		}
	}
	if codeOk {
		out.add("response_type: code", CheckPass, "supported")
	} else {
		out.add("response_type: code", CheckFail, "authorization code flow not supported")
	}

	s256 := false
	for _, m := range doc.CodeChallengeMethodsSupported {
		if m == "S256" {
			s256 = true
		}
	}
	if s256 {
		out.add("PKCE S256", CheckPass, "supported")
	} else {
		out.add("PKCE S256", CheckWarn, "not advertised — BookLore sends S256 anyway")
	}

	if doc.JWKSURI != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, doc.JWKSURI, nil)
		jresp, err := cli.Do(req)
		if err != nil {
			out.add("JWKS fetch", CheckFail, err.Error())
		} else {
			defer func() { _ = jresp.Body.Close() }()
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

// -----------------------------------------------------------------------------
// State cache
// -----------------------------------------------------------------------------

type stateEntry struct {
	Nonce        string
	CodeVerifier string
	CreatedAt    time.Time
	ProviderSlug string
	// RedirectURL is the exact redirect_uri sent to the IdP when the
	// authorize URL was built. The callback must pass the same value to
	// the token endpoint (per OAuth2 spec) and our redirect URL depends
	// on the request origin when APP_URL is unset, so we stash it here.
	RedirectURL string
}

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

// -----------------------------------------------------------------------------
// Small helpers
// -----------------------------------------------------------------------------

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

func orString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
