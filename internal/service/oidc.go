// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	ErrOIDCPendingApproval  = errors.New("this OIDC account is awaiting administrator approval")

	// ErrOIDCEmailClaimMissing is the refusal for an IdP that completed
	// the exchange but returned no email claim, which this instance
	// requires to create an account. A sentinel rather than an inline
	// error because it is the one refusal an admin can fix: it means the
	// provider's claim mapping is wrong (or the scope is too narrow), and
	// the handler has to be able to say so instead of reporting a generic
	// failure the operator cannot act on.
	ErrOIDCEmailClaimMissing = errors.New("the OIDC provider returned no email claim and this instance requires one")

	// ErrOIDCUnknownProvisionStatus is returned when the Provisioner
	// reports a status refuseLogin has no arm for. It is a programming
	// error — someone added a ProvisionStatus and did not teach the OIDC
	// adapter what it means — so it is deliberately not one of the login
	// refusals: absorbing it would turn that omission into users being
	// quietly turned away with no trace of why.
	ErrOIDCUnknownProvisionStatus = errors.New("oidc: unhandled provisioning status")
)

const (
	// providerCacheTTL is how long the discovery result for a generic
	// OIDC issuer is kept before re-fetching. Admins saving settings
	// also call Invalidate to bust it explicitly.
	providerCacheTTL = 1 * time.Hour

	// stateTTL is the window in which a /login must complete the
	// round-trip back to /callback. Matches the spec's 5-min guidance.
	stateTTL = 5 * time.Minute

	// githubIssuer is the issuer stamped on identities from the GitHub
	// flow. GitHub is not an OIDC provider — there is no discovery
	// document to report one — so it is a constant here rather than
	// something the exchange returns.
	githubIssuer = "https://github.com"
)

// oidcProvider is one login provider's two operations. Both the
// authorize-URL builder and the callback exchange dispatch on the same
// slug, so they are declared together and looked up once rather than
// switched on in two places that must stay in step.
//
// Shaped as a struct of funcs rather than an interface for the same
// reason queue/registry.go is: the per-provider work already lives as
// methods, so a registration is a pair of method values and no bodies
// have to move. Adding a provider is one entry in newProviderRegistry.
type oidcProvider struct {
	authURL  func(ctx context.Context, redirect, intent, linkUserID string) (string, error)
	callback func(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error)
}

// OIDCService is the multi-provider OIDC/OAuth login service.
// Google, GitHub, and a custom OIDC provider each have their own
// settings row and can be enabled in parallel.
// The narrow interfaces below are the slices of each repo this service
// uses. They are what the constructor takes, so the login flow's own
// logic — state handling, intent dispatch, the foreign-identity check,
// the provisioning-status switch — is testable without a database, the
// same way Provisioner's three seams made its policy testable.
//
// Each one covers what the *flow* calls, which includes what it calls
// through the Provisioner it builds: Exchange's login arm is Provision
// plus a session, so the Provisioner's own seams are embedded here
// rather than asking the composition root to build it separately.

// oidcSettingsStore is the settings surface: the three provider config
// rows, the force-only toggle, and (via the Provisioner) the
// auto-provisioning policy row.
type oidcSettingsStore interface {
	provisionerSettings

	GetGoogle(ctx context.Context) (repo.OAuthPresetConfig, error)
	GetGitHub(ctx context.Context) (repo.OAuthPresetConfig, error)
	GetGenericOIDC(ctx context.Context) (repo.GenericOIDCConfig, error)
	GetBool(ctx context.Context, name string) (bool, error)
}

// oidcUserProfileStore is the one user-facing write the callback makes
// beyond provisioning — refreshing name/avatar from the IdP's claims —
// plus the user surface the Provisioner needs to resolve or create the
// account being logged in.
type oidcUserProfileStore interface {
	provisionerUsers

	SyncOIDCProfile(ctx context.Context, userID, name, avatarURL string) error
}

// oidcSessionStore mints the browser session after a successful login.
// Implemented by *repo.SessionRepo.
type oidcSessionStore interface {
	Create(ctx context.Context, userID, userAgent string, ttl time.Duration) (model.Session, error)
}

// oidcIdentityStore is the user_identities surface: the link flow's own
// three calls — GetByIssuerSubject, Insert, TouchLastLogin — plus the
// rest of the table the Provisioner touches on the login arm.
type oidcIdentityStore interface {
	provisionerIdentities
}

type OIDCService struct {
	appURL     string
	settings   oidcSettingsStore
	users      oidcUserProfileStore
	sessions   oidcSessionStore
	identities oidcIdentityStore
	prov       *Provisioner

	states *stateStore

	// providers is the slug → operations registry. Built once at
	// construction; the two dispatch sites are map lookups.
	providers map[string]oidcProvider

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

// NewOIDCService takes the narrow stores above; *repo.AppSettingsRepo,
// *repo.UserRepo, *repo.SessionRepo and *repo.IdentityRepo satisfy them
// at the composition root. The Provisioner is built here from the same
// three values, so the whole callback path can run against fakes.
func NewOIDCService(settings oidcSettingsStore, users oidcUserProfileStore, sessions oidcSessionStore, identities oidcIdentityStore, appURL string) *OIDCService {
	svc := &OIDCService{
		appURL:     strings.TrimRight(appURL, "/"),
		settings:   settings,
		users:      users,
		sessions:   sessions,
		identities: identities,
		prov:       NewProvisioner(settings, users, identities),
		states:     newStateStore(),
	}
	svc.providers = svc.newProviderRegistry()
	return svc
}

// newProviderRegistry declares every login provider this binary supports.
// One entry per slug; nothing else in the file switches on a slug.
func (s *OIDCService) newProviderRegistry() map[string]oidcProvider {
	return map[string]oidcProvider{
		repo.ProviderSlugGoogle: {
			authURL:  s.authURLGoogleWithIntent,
			callback: s.callbackGoogle,
		},
		repo.ProviderSlugGitHub: {
			authURL:  s.authURLGitHubWithIntent,
			callback: s.callbackGitHub,
		},
		repo.ProviderSlugGeneric: {
			authURL:  s.authURLGenericWithIntent,
			callback: s.callbackGeneric,
		},
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
	return s.authURLForSlug(ctx, slug, redirect, IntentLogin, "")
}

// ExchangeOutcome is the discriminated result of completing an OIDC
// callback. Intent reflects how the flow was started; login outcomes
// populate Session+User, link outcomes populate Provider+UserID.
//
// One error is returned *with* a populated outcome, and it is the only
// one: ErrOIDCPendingApproval carries Intent+User, because the account
// exists and awaits approval, and the landing page the handler redirects
// to has nothing to render without the user. The ordinary Go habit of
// discarding the value when err != nil therefore drops something real
// here. Every other refusal — state mismatch, link user mismatch,
// denied, not allowed, missing email claim, an unhandled provisioning
// status, or any store failure — returns the zero outcome, which is
// what makes the exception safe to remember.
type ExchangeOutcome struct {
	Intent   string // IntentLogin | IntentLink
	Session  model.Session
	User     model.User
	Provider string
	UserID   string
	Email    string
}

// ErrOIDCLinkUserMismatch is returned when a link callback fires with
// a session whose user does not match the user that initiated the
// link. The handler maps this to a redirect with ?error=session_expired.
var ErrOIDCLinkUserMismatch = errors.New("oidc: link callback user mismatch")

// Exchange completes the callback for either a login flow or a link
// flow, dispatching on state.Intent. For link callbacks the caller
// must pass the current session's user ID (empty for login); the
// service verifies it matches the user that initiated the link.
func (s *OIDCService) Exchange(ctx context.Context, code, state, userAgent, sessionUserID string) (ExchangeOutcome, error) {
	entry, ok := s.states.take(state)
	if !ok {
		return ExchangeOutcome{}, ErrOIDCStateMismatch
	}

	intent := entry.Intent
	if intent == "" {
		intent = IntentLogin
	}
	if intent == IntentLink && entry.LinkUserID != sessionUserID {
		return ExchangeOutcome{}, ErrOIDCLinkUserMismatch
	}

	// The redirect_uri replayed to the token endpoint must be the exact
	// one the authorize request carried, which is why the state stamps it.
	// An empty one is not something to paper over: it would reach the IdP
	// as a blank redirect_uri and come back as an error naming nothing an
	// operator could act on. Both AuthURL and AuthURLForLink already
	// refuse with this sentinel before minting such a state, so this is
	// the same refusal at the other end of the round trip rather than a
	// second policy.
	redirect := entry.RedirectURL
	if redirect == "" {
		return ExchangeOutcome{}, ErrOIDCNotConfigured
	}

	claims, issuer, err := s.resolveCallback(ctx, code, entry, redirect)
	if err != nil {
		return ExchangeOutcome{}, err
	}

	if intent == IntentLink {
		// Panel-driven link: bind the IdP-attested identity to the
		// signed-in user. The callback already proved possession of
		// the IdP account; we still sanity-check the (issuer, subject)
		// pair isn't claimed by another user.
		if existing, gerr := s.identities.GetByIssuerSubject(ctx, issuer, claims.Subject); gerr == nil {
			if existing.UserID != sessionUserID {
				return ExchangeOutcome{}, repo.ErrIdentityForeignUser
			}
			// Already linked to this user — idempotent success.
			_ = s.identities.TouchLastLogin(ctx, existing.ID)
			return ExchangeOutcome{
				Intent: IntentLink, Provider: entry.ProviderSlug,
				UserID: sessionUserID, Email: claims.Email,
			}, nil
		} else if !errors.Is(gerr, repo.ErrNotFound) {
			return ExchangeOutcome{}, gerr
		}
		if _, err := s.identities.Insert(ctx, sessionUserID, entry.ProviderSlug, issuer, claims.Subject, claims.Email); err != nil {
			return ExchangeOutcome{}, err
		}
		return ExchangeOutcome{
			Intent: IntentLink, Provider: entry.ProviderSlug,
			UserID: sessionUserID, Email: claims.Email,
		}, nil
	}

	res, err := s.prov.Provision(ctx, ExternalIdentity{
		Provider: entry.ProviderSlug,
		Issuer:   issuer,
		Subject:  claims.Subject,
		Email:    claims.Email,
		Name:     claims.Name,
	})
	if err != nil {
		return ExchangeOutcome{}, err
	}
	if res.Status != ProvisionResolved {
		return refuseLogin(res)
	}
	u := res.User
	_ = s.users.SyncOIDCProfile(ctx, u.ID, claims.Name, claims.Picture)

	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return ExchangeOutcome{}, err
	}
	return ExchangeOutcome{Intent: IntentLogin, Session: sess, User: u}, nil
}

// refuseLogin maps a non-resolved provisioning status to the pair
// Exchange returns for it. Callers handle ProvisionResolved themselves —
// there is no session to mint here.
func refuseLogin(res ProvisionResult) (ExchangeOutcome, error) {
	switch res.Status {
	case ProvisionPendingApproval:
		// The one refusal that returns a populated outcome; see
		// ExchangeOutcome's doc for why the caller must keep it.
		return ExchangeOutcome{Intent: IntentLogin, User: res.User}, ErrOIDCPendingApproval
	case ProvisionEmailRequired:
		return ExchangeOutcome{}, ErrOIDCEmailClaimMissing
	case ProvisionDenied, ProvisionNotAllowed:
		// Flattened on purpose, and the flattening is the point: an
		// account an admin denied and an identity no policy admits must
		// be one indistinguishable refusal, or the login page reports
		// whether the account exists to anyone who asks.
		return ExchangeOutcome{}, ErrOIDCLoginNotAllowed
	default:
		// Not a refusal — a status this adapter was never taught. Names
		// the status (a fact about the code) and nothing about the user
		// it arrived with, so a loud failure stays a safe one.
		return ExchangeOutcome{}, fmt.Errorf("%w %q", ErrOIDCUnknownProvisionStatus, res.Status)
	}
}

// resolveCallback runs the provider-specific OAuth/OIDC token exchange
// and returns the resolved claims + canonical issuer for the request.
func (s *OIDCService) resolveCallback(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	p, ok := s.providers[entry.ProviderSlug]
	if !ok {
		return resolvedClaims{}, "", ErrOIDCUnknownProvider
	}
	return p.callback(ctx, code, entry, redirect)
}

// callbackGoogle exchanges the code against Google's OIDC endpoints.
func (s *OIDCService) callbackGoogle(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	cfg, err := s.settings.GetGoogle(ctx)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	if !googleUsable(cfg) {
		return resolvedClaims{}, "", ErrOIDCDisabled
	}
	return s.oidcCallback(ctx, code, entry, googleOIDCConfig(cfg), redirect)
}

// callbackGitHub exchanges the code against GitHub's REST API. GitHub is
// not an OIDC provider — no discovery document, no ID token — so the
// issuer is a constant rather than something discovery reports.
func (s *OIDCService) callbackGitHub(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	cfg, err := s.settings.GetGitHub(ctx)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	if !githubUsable(cfg) {
		return resolvedClaims{}, "", ErrOIDCDisabled
	}
	claims, err := s.githubCallback(ctx, code, entry, cfg, redirect)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	return claims, githubIssuer, nil
}

// callbackGeneric exchanges the code against the admin-configured issuer.
func (s *OIDCService) callbackGeneric(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	cfg, err := s.settings.GetGenericOIDC(ctx)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	if !genericUsable(cfg) {
		return resolvedClaims{}, "", ErrOIDCDisabled
	}
	return s.oidcCallback(ctx, code, entry, cfg, redirect)
}

// AuthURLForLink builds an authorize URL whose state carries
// Intent=link and the initiating user's ID. The callback uses these
// to bind the resulting identity to that user instead of issuing a
// new session.
func (s *OIDCService) AuthURLForLink(ctx context.Context, slug, baseURL, userID string) (string, error) {
	if userID == "" {
		return "", errors.New("oidc: link auth URL requires a user id")
	}
	redirect := s.resolveRedirectURL(baseURL)
	if redirect == "" {
		return "", ErrOIDCNotConfigured
	}
	return s.authURLForSlug(ctx, slug, redirect, IntentLink, userID)
}

// authURLForSlug is the shared dispatch used by both AuthURL and
// AuthURLForLink — same builders, same state minting, same redirect.
func (s *OIDCService) authURLForSlug(ctx context.Context, slug, redirect, intent, linkUserID string) (string, error) {
	p, ok := s.providers[slug]
	if !ok {
		return "", ErrOIDCUnknownProvider
	}
	return p.authURL(ctx, redirect, intent, linkUserID)
}

// -----------------------------------------------------------------------------
// AuthURL builders
// -----------------------------------------------------------------------------

func (s *OIDCService) authURLGoogleWithIntent(ctx context.Context, redirect, intent, linkUserID string) (string, error) {
	cfg, err := s.settings.GetGoogle(ctx)
	if err != nil {
		return "", err
	}
	if !googleUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return s.authURLOIDC(ctx, repo.ProviderSlugGoogle, googleOIDCConfig(cfg), redirect, intent, linkUserID)
}

func (s *OIDCService) authURLGenericWithIntent(ctx context.Context, redirect, intent, linkUserID string) (string, error) {
	cfg, err := s.settings.GetGenericOIDC(ctx)
	if err != nil {
		return "", err
	}
	if !genericUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return s.authURLOIDC(ctx, repo.ProviderSlugGeneric, cfg, redirect, intent, linkUserID)
}

func (s *OIDCService) authURLOIDC(ctx context.Context, slug string, cfg repo.GenericOIDCConfig, redirect, intent, linkUserID string) (string, error) {
	disc, err := s.getDiscovery(ctx, cfg)
	if err != nil {
		return "", err
	}
	state, nonce, verifier, err := s.issueStateWithIntent(slug, redirect, intent, linkUserID)
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

func (s *OIDCService) authURLGitHubWithIntent(ctx context.Context, redirect, intent, linkUserID string) (string, error) {
	cfg, err := s.settings.GetGitHub(ctx)
	if err != nil {
		return "", err
	}
	if !githubUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	state, _, verifier, err := s.issueStateWithIntent(repo.ProviderSlugGitHub, redirect, intent, linkUserID)
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
	return githubIssuer + "/login/oauth/authorize?" + v.Encode(), nil
}

func (s *OIDCService) issueStateWithIntent(slug, redirect, intent, linkUserID string) (state, nonce, verifier string, err error) {
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
		Intent:       intent,
		LinkUserID:   linkUserID,
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
	// Intent discriminates login vs link flows so the callback can
	// branch without re-deriving the user's intent from cookies. Empty
	// string defaults to "login".
	Intent string
	// LinkUserID is set only on link flows so the callback can verify
	// the session-bound user matches the user that initiated the link.
	LinkUserID string
}

// IntentLogin and IntentLink discriminate the two callback paths.
const (
	IntentLogin = "login"
	IntentLink  = "link"
)

// stateStore ties an authorize redirect to the callback that returns:
// it holds the PKCE verifier, the nonce, the provider slug, and the exact
// redirect_uri that was sent. A state is single-use — take deletes it —
// so a replayed callback finds nothing, and entries older than stateTTL
// are reaped on write rather than on a timer.
//
// It is deliberately in-process, which makes this service single-instance
// for login: two replicas behind a load balancer will fail any callback
// that lands on the replica that did not mint the state. Sharing it would
// mean moving state into a signed cookie or a table — a real design
// change, not a tidy-up. Until then, run one instance, or pin OIDC
// callbacks to one.
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
