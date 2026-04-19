package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

var (
	ErrOIDCNotConfigured = errors.New("OIDC is not configured")
	ErrOIDCStateMismatch = errors.New("OIDC state mismatch")
)

// OIDCService handles the OpenID Connect authorization code flow.
type OIDCService struct {
	cfg      config.Config
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	users    *repo.UserRepo
	sessions *repo.SessionRepo
}

// NewOIDCService initialises the OIDC provider by performing discovery on
// the issuer URL. Returns (nil, nil) when OIDC is not configured.
func NewOIDCService(ctx context.Context, cfg config.Config, users *repo.UserRepo, sessions *repo.SessionRepo) (*OIDCService, error) {
	if !cfg.OIDCEnabled() {
		return nil, nil
	}

	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, err
	}

	oauthCfg := oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.OIDCRedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})

	return &OIDCService{
		cfg:      cfg,
		provider: provider,
		verifier: verifier,
		oauth:    oauthCfg,
		users:    users,
		sessions: sessions,
	}, nil
}

// AuthURL returns the provider's authorization URL and a random state value
// the caller must persist (cookie) and verify on callback.
func (s *OIDCService) AuthURL() (authURL, state, nonce string, err error) {
	state, err = randomString(32)
	if err != nil {
		return "", "", "", err
	}
	nonce, err = randomString(32)
	if err != nil {
		return "", "", "", err
	}
	authURL = s.oauth.AuthCodeURL(state, oidc.Nonce(nonce))
	return authURL, state, nonce, nil
}

// OIDCClaims are the claims extracted from the ID token.
type OIDCClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

// Exchange trades the authorization code for tokens, verifies the ID token,
// finds or creates a local user, and issues a session.
func (s *OIDCService) Exchange(ctx context.Context, code, nonce, userAgent string) (model.Session, model.User, error) {
	token, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return model.Session{}, model.User{}, errors.New("no id_token in token response")
	}

	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return model.Session{}, model.User{}, err
	}

	if idToken.Nonce != nonce {
		return model.Session{}, model.User{}, errors.New("nonce mismatch")
	}

	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		return model.Session{}, model.User{}, err
	}

	claims.Email = strings.TrimSpace(strings.ToLower(claims.Email))
	if claims.Email == "" {
		return model.Session{}, model.User{}, errors.New("OIDC provider did not return an email claim")
	}

	issuer := s.cfg.OIDCIssuerURL

	// 1) Try to find user by OIDC identity.
	u, err := s.users.GetByOIDC(ctx, issuer, claims.Subject)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return model.Session{}, model.User{}, err
	}

	if errors.Is(err, repo.ErrNotFound) {
		// 2) Try to find by email and link.
		u, err = s.users.GetByEmail(ctx, claims.Email)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return model.Session{}, model.User{}, err
		}
		if errors.Is(err, repo.ErrNotFound) {
			// 3) Auto-provision new user.
			role := model.RoleUser
			// If no users exist yet, make the first OIDC user an admin.
			n, countErr := s.users.Count(ctx)
			if countErr != nil {
				return model.Session{}, model.User{}, countErr
			}
			if n == 0 {
				role = model.RoleAdmin
			}
			u, err = s.users.CreateOIDC(ctx, claims.Email, claims.Name, role, issuer, claims.Subject)
			if err != nil {
				return model.Session{}, model.User{}, err
			}
		} else {
			// Link existing email user to OIDC.
			if err := s.users.LinkOIDC(ctx, u.ID, issuer, claims.Subject); err != nil {
				return model.Session{}, model.User{}, err
			}
		}
	}

	// Issue session.
	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())

	return sess, u, nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
