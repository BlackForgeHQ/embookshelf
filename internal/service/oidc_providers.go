// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"

	"github.com/blackforge/embookshelf/internal/repo"
)

// One login provider, whole (#258). Before this file a provider existed
// five times — its usable gate, its auth-URL builder, its callback, its
// public listing, and a hand-written slug switch in the handler for the
// connection test. Each of those sites dispatched on the same slug and
// had to stay in step. Now a provider is one adapter satisfying this
// interface, and adding one is a single entry in newProviderRegistry:
// the login page, the dispatch, and the test endpoint all derive from
// the registry.
//
// The adapters hold *OIDCService because the mechanics they share —
// discovery cache, state minting, the OAuth exchanges — live there; the
// adapter is the provider-shaped face over that machinery, not a second
// home for it.
type oidcProvider interface {
	// public is the usable gate and the login-page entry in one: a
	// provider that is enabled and fully configured returns its listing
	// and true, anything else returns false. Merged deliberately —
	// "usable" exists so the login page doesn't offer a button that
	// 500s, so the gate and the listing are one decision.
	public(ctx context.Context) (PublicProvider, bool, error)

	// authURL builds the authorize URL for a login or link flow,
	// minting the state that ties the redirect to the callback.
	authURL(ctx context.Context, redirect, intent, linkUserID string) (string, error)

	// callback runs the provider's token exchange and returns the
	// resolved claims plus the canonical issuer for the identity row.
	callback(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error)

	// test runs the admin panel's connection diagnostic. body is the
	// raw request payload — each provider owns its override shape, and
	// a blank submission falls back to the stored row (the one rule,
	// stated once, in testWithStoredFallback).
	test(ctx context.Context, body []byte) (TestResult, error)
}

// registeredProvider pairs a slug with its adapter. The registry is an
// ordered slice rather than a map so the login page lists providers in
// a stable order and the enumeration test can walk it.
type registeredProvider struct {
	slug string
	oidcProvider
}

// newProviderRegistry declares every login provider this binary
// supports. One entry per provider; nothing else switches on a slug.
func (s *OIDCService) newProviderRegistry() []registeredProvider {
	return []registeredProvider{
		{repo.ProviderSlugGoogle, googleProvider{s}},
		{repo.ProviderSlugGitHub, githubProvider{s}},
		{repo.ProviderSlugGeneric, genericProvider{s}},
	}
}

// provider resolves a slug against the registry. Linear over three
// entries; the slice is the single source of truth, so there is no map
// to fall out of step with.
func (s *OIDCService) provider(slug string) (oidcProvider, bool) {
	for _, e := range s.providers {
		if e.slug == slug {
			return e.oidcProvider, true
		}
	}
	return nil, false
}

// oidcLoginURL is the path the login page's button hits for a slug.
func oidcLoginURL(slug string) string {
	return "/api/v1/auth/oidc/" + slug
}

// presetUsable gates the two preset providers (Google, GitHub): enabled
// with both credentials present. genericUsable differs because public
// clients are legitimate there — it needs an issuer, not a secret.
func presetUsable(c repo.OAuthPresetConfig) bool {
	return c.Enabled && c.ClientID != "" && c.ClientSecret != ""
}

func genericUsable(c repo.GenericOIDCConfig) bool {
	return c.Enabled && c.ClientID != "" && c.IssuerURI != ""
}

// testWithStoredFallback is the blank-submission rule, stated once: the
// panel tests what is on screen, and a submission missing its required
// fields means "test what is stored instead". A failure to read that
// row is a store error, not a diagnostic verdict.
func testWithStoredFallback[C any](
	ctx context.Context,
	override C,
	blank func(C) bool,
	stored func(context.Context) (C, error),
	run func(context.Context, C) TestResult,
) (TestResult, error) {
	cfg := override
	if blank(cfg) {
		var err error
		if cfg, err = stored(ctx); err != nil {
			return TestResult{}, err
		}
	}
	return run(ctx, cfg), nil
}

// oauthPresetOverride is the wire shape both preset providers' test
// submissions carry. Bind errors are deliberately ignored, matching the
// old handler: an unreadable body is a blank submission, which the
// fallback rule already answers.
type oauthPresetOverride struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (o oauthPresetOverride) config() repo.OAuthPresetConfig {
	return repo.OAuthPresetConfig{ClientID: o.ClientID, ClientSecret: o.ClientSecret}
}

func presetBlank(c repo.OAuthPresetConfig) bool { return c.ClientID == "" }

// -----------------------------------------------------------------------------
// Google
// -----------------------------------------------------------------------------

type googleProvider struct{ s *OIDCService }

func (p googleProvider) public(ctx context.Context) (PublicProvider, bool, error) {
	cfg, err := p.s.settings.GetGoogle(ctx)
	if err != nil || !presetUsable(cfg) {
		return PublicProvider{}, false, err
	}
	return PublicProvider{
		Slug: repo.ProviderSlugGoogle, Name: "Google", Kind: "google",
		LoginURL: oidcLoginURL(repo.ProviderSlugGoogle),
	}, true, nil
}

func (p googleProvider) authURL(ctx context.Context, redirect, intent, linkUserID string) (string, error) {
	cfg, err := p.s.settings.GetGoogle(ctx)
	if err != nil {
		return "", err
	}
	if !presetUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return p.s.authURLOIDC(ctx, repo.ProviderSlugGoogle, googleOIDCConfig(cfg), redirect, intent, linkUserID)
}

func (p googleProvider) callback(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	cfg, err := p.s.settings.GetGoogle(ctx)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	if !presetUsable(cfg) {
		return resolvedClaims{}, "", ErrOIDCDisabled
	}
	return p.s.oidcCallback(ctx, code, entry, googleOIDCConfig(cfg), redirect)
}

func (p googleProvider) test(ctx context.Context, body []byte) (TestResult, error) {
	var b struct {
		Google oauthPresetOverride `json:"google"`
	}
	_ = json.Unmarshal(body, &b)
	return testWithStoredFallback(ctx, b.Google.config(), presetBlank, p.s.settings.GetGoogle, testGoogle)
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

// -----------------------------------------------------------------------------
// GitHub
// -----------------------------------------------------------------------------

type githubProvider struct{ s *OIDCService }

func (p githubProvider) public(ctx context.Context) (PublicProvider, bool, error) {
	cfg, err := p.s.settings.GetGitHub(ctx)
	if err != nil || !presetUsable(cfg) {
		return PublicProvider{}, false, err
	}
	return PublicProvider{
		Slug: repo.ProviderSlugGitHub, Name: "GitHub", Kind: "github",
		LoginURL: oidcLoginURL(repo.ProviderSlugGitHub),
	}, true, nil
}

func (p githubProvider) authURL(ctx context.Context, redirect, intent, linkUserID string) (string, error) {
	cfg, err := p.s.settings.GetGitHub(ctx)
	if err != nil {
		return "", err
	}
	if !presetUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return p.s.authURLGitHub(cfg, redirect, intent, linkUserID)
}

func (p githubProvider) callback(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	cfg, err := p.s.settings.GetGitHub(ctx)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	if !presetUsable(cfg) {
		return resolvedClaims{}, "", ErrOIDCDisabled
	}
	claims, err := p.s.githubCallback(ctx, code, entry, cfg, redirect)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	return claims, githubIssuer, nil
}

func (p githubProvider) test(ctx context.Context, body []byte) (TestResult, error) {
	var b struct {
		GitHub oauthPresetOverride `json:"github"`
	}
	_ = json.Unmarshal(body, &b)
	return testWithStoredFallback(ctx, b.GitHub.config(), presetBlank, p.s.settings.GetGitHub, testGitHub)
}

// -----------------------------------------------------------------------------
// Generic OIDC
// -----------------------------------------------------------------------------

type genericProvider struct{ s *OIDCService }

func (p genericProvider) public(ctx context.Context) (PublicProvider, bool, error) {
	cfg, err := p.s.settings.GetGenericOIDC(ctx)
	if err != nil || !genericUsable(cfg) {
		return PublicProvider{}, false, err
	}
	name := cfg.ProviderName
	if name == "" {
		name = "SSO"
	}
	return PublicProvider{
		Slug: repo.ProviderSlugGeneric, Name: name, Kind: "oidc",
		LoginURL: oidcLoginURL(repo.ProviderSlugGeneric),
	}, true, nil
}

func (p genericProvider) authURL(ctx context.Context, redirect, intent, linkUserID string) (string, error) {
	cfg, err := p.s.settings.GetGenericOIDC(ctx)
	if err != nil {
		return "", err
	}
	if !genericUsable(cfg) {
		return "", ErrOIDCDisabled
	}
	return p.s.authURLOIDC(ctx, repo.ProviderSlugGeneric, cfg, redirect, intent, linkUserID)
}

func (p genericProvider) callback(ctx context.Context, code string, entry stateEntry, redirect string) (resolvedClaims, string, error) {
	cfg, err := p.s.settings.GetGenericOIDC(ctx)
	if err != nil {
		return resolvedClaims{}, "", err
	}
	if !genericUsable(cfg) {
		return resolvedClaims{}, "", ErrOIDCDisabled
	}
	return p.s.oidcCallback(ctx, code, entry, cfg, redirect)
}

func (p genericProvider) test(ctx context.Context, body []byte) (TestResult, error) {
	var b struct {
		Generic struct {
			ClientID     string            `json:"clientId"`
			ClientSecret string            `json:"clientSecret"`
			IssuerURI    string            `json:"issuerUri"`
			Scopes       string            `json:"scopes"`
			ClaimMapping repo.ClaimMapping `json:"claimMapping"`
		} `json:"generic"`
	}
	_ = json.Unmarshal(body, &b)
	override := repo.GenericOIDCConfig{
		ClientID:     b.Generic.ClientID,
		ClientSecret: b.Generic.ClientSecret,
		IssuerURI:    b.Generic.IssuerURI,
		Scopes:       b.Generic.Scopes,
		ClaimMapping: b.Generic.ClaimMapping,
	}
	return testWithStoredFallback(ctx, override,
		func(c repo.GenericOIDCConfig) bool { return c.ClientID == "" || c.IssuerURI == "" },
		p.s.settings.GetGenericOIDC, testGeneric)
}
