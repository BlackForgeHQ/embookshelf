// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// Admin settings DTOs. The wire shape keeps the three provider configs
// in one round-trip so the settings UI renders without a fan-out.
type oidcSettingsDTO struct {
	ForceOnly     bool                          `json:"forceOnly"`
	AutoProvision repo.OIDCAutoProvisionDetails `json:"autoProvision"`
	Google        oauthPresetDTO                `json:"google"`
	GitHub        oauthPresetDTO                `json:"github"`
	Generic       genericOIDCDTO                `json:"generic"`
	RedirectURI   string                        `json:"redirectUri"`
}

type oauthPresetDTO struct {
	Enabled         bool   `json:"enabled"`
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret,omitempty"`
	ClientSecretSet bool   `json:"clientSecretSet"`
}

type genericOIDCDTO struct {
	Enabled         bool              `json:"enabled"`
	ProviderName    string            `json:"providerName"`
	ClientID        string            `json:"clientId"`
	ClientSecret    string            `json:"clientSecret,omitempty"`
	ClientSecretSet bool              `json:"clientSecretSet"`
	IssuerURI       string            `json:"issuerUri"`
	Scopes          string            `json:"scopes"`
	ClaimMapping    repo.ClaimMapping `json:"claimMapping"`
}

// SettingsOIDCGet returns everything the OIDC settings page needs.
func (h *Handler) SettingsOIDCGet(c *gin.Context) {
	if h.appSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return
	}
	ctx := c.Request.Context()

	force, err := h.appSettings.GetBool(ctx, repo.SettingOIDCForceOnlyMode)
	if err != nil {
		writeServerError(c, "oidc settings force", err)
		return
	}
	auto, err := h.appSettings.GetOIDCAutoProvision(ctx)
	if err != nil {
		writeServerError(c, "oidc settings auto", err)
		return
	}
	google, err := h.appSettings.GetGoogle(ctx)
	if err != nil {
		writeServerError(c, "oidc google", err)
		return
	}
	github, err := h.appSettings.GetGitHub(ctx)
	if err != nil {
		writeServerError(c, "oidc github", err)
		return
	}
	generic, err := h.appSettings.GetGenericOIDC(ctx)
	if err != nil {
		writeServerError(c, "oidc generic", err)
		return
	}

	c.JSON(http.StatusOK, oidcSettingsDTO{
		ForceOnly:     force,
		AutoProvision: auto,
		Google:        presetToDTO(google),
		GitHub:        presetToDTO(github),
		Generic:       genericToDTO(generic),
		RedirectURI:   buildRedirectURI(c),
	})
}

// SettingsOIDCUpdate accepts the full DTO and saves each provider's
// row plus the shared flags. ClientSecret is preserved when absent —
// an admin editing the client ID shouldn't have to re-enter the secret.
func (h *Handler) SettingsOIDCUpdate(c *gin.Context) {
	if h.appSettings == nil || h.oidcSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return
	}
	var body oidcSettingsDTO
	if !bindJSON(c, &body) {
		return
	}
	ctx := c.Request.Context()

	// The keep-versus-clear contract for secrets stays here, with the
	// wire shape that expresses it: a blank field with its "set" flag
	// still true means "keep what is stored". What the service receives
	// is already-resolved plaintext (ADR-0010).
	sub, err := h.resolveOIDCSubmission(ctx, body)
	if err != nil {
		writeServerError(c, "oidc existing rows", err)
		return
	}

	if err := h.oidcSettings.Apply(ctx, sub); err != nil {
		switch {
		case errors.Is(err, service.ErrOIDCIncomplete),
			errors.Is(err, service.ErrOIDCForceOnlyBlocked):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "oidc settings apply", err)
		}
		return
	}

	if h.oidc != nil {
		h.oidc.Invalidate()
	}
	h.SettingsOIDCGet(c)
}

// resolveOIDCSubmission turns the wire shape into the submission the
// service applies, reading the stored rows only to resolve secrets the
// client asked to keep.
func (h *Handler) resolveOIDCSubmission(
	ctx context.Context,
	body oidcSettingsDTO,
) (service.OIDCSubmission, error) {
	var zero service.OIDCSubmission

	existingGoogle, err := h.appSettings.GetGoogle(ctx)
	if err != nil {
		return zero, err
	}
	existingGitHub, err := h.appSettings.GetGitHub(ctx)
	if err != nil {
		return zero, err
	}
	existingGeneric, err := h.appSettings.GetGenericOIDC(ctx)
	if err != nil {
		return zero, err
	}

	return service.OIDCSubmission{
		Google: repo.OAuthPresetConfig{
			Enabled:      body.Google.Enabled,
			ClientID:     strings.TrimSpace(body.Google.ClientID),
			ClientSecret: resolveSecret(body.Google.ClientSecret, body.Google.ClientSecretSet, existingGoogle.ClientSecret),
		},
		GitHub: repo.OAuthPresetConfig{
			Enabled:      body.GitHub.Enabled,
			ClientID:     strings.TrimSpace(body.GitHub.ClientID),
			ClientSecret: resolveSecret(body.GitHub.ClientSecret, body.GitHub.ClientSecretSet, existingGitHub.ClientSecret),
		},
		Generic: repo.GenericOIDCConfig{
			Enabled:      body.Generic.Enabled,
			ProviderName: strings.TrimSpace(body.Generic.ProviderName),
			ClientID:     strings.TrimSpace(body.Generic.ClientID),
			ClientSecret: resolveSecret(body.Generic.ClientSecret, body.Generic.ClientSecretSet, existingGeneric.ClientSecret),
			IssuerURI:    strings.TrimSpace(body.Generic.IssuerURI),
			Scopes:       strings.TrimSpace(body.Generic.Scopes),
			// Defaults are the Setting declaration's, so a row written by
			// anything other than this endpoint gets them too (#195).
			ClaimMapping: body.Generic.ClaimMapping,
		},
		AutoProvision: body.AutoProvision,
		ForceOnly:     body.ForceOnly,
	}, nil
}

// SettingsOIDCTest runs TestConnection for one provider at a time. The
// slug is supplied in the URL; the body carries an optional override
// config (useful when testing before saving).
func (h *Handler) SettingsOIDCTest(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusServiceUnavailable, "oidc service unavailable")
		return
	}
	slug := c.Param("slug")
	ctx := c.Request.Context()

	switch slug {
	case repo.ProviderSlugGoogle:
		var body struct {
			Google oauthPresetDTO `json:"google"`
		}
		_ = c.ShouldBindJSON(&body)
		cfg := repo.OAuthPresetConfig{
			ClientID:     body.Google.ClientID,
			ClientSecret: body.Google.ClientSecret,
		}
		if cfg.ClientID == "" {
			stored, err := h.appSettings.GetGoogle(ctx)
			if err != nil {
				writeServerError(c, "oidc test google", err)
				return
			}
			cfg = stored
		}
		c.JSON(http.StatusOK, h.oidc.TestGoogle(ctx, cfg))

	case repo.ProviderSlugGitHub:
		var body struct {
			GitHub oauthPresetDTO `json:"github"`
		}
		_ = c.ShouldBindJSON(&body)
		cfg := repo.OAuthPresetConfig{
			ClientID:     body.GitHub.ClientID,
			ClientSecret: body.GitHub.ClientSecret,
		}
		if cfg.ClientID == "" {
			stored, err := h.appSettings.GetGitHub(ctx)
			if err != nil {
				writeServerError(c, "oidc test github", err)
				return
			}
			cfg = stored
		}
		c.JSON(http.StatusOK, h.oidc.TestGitHub(ctx, cfg))

	case repo.ProviderSlugGeneric:
		var body struct {
			Generic genericOIDCDTO `json:"generic"`
		}
		_ = c.ShouldBindJSON(&body)
		cfg := repo.GenericOIDCConfig{
			ClientID:     body.Generic.ClientID,
			ClientSecret: body.Generic.ClientSecret,
			IssuerURI:    body.Generic.IssuerURI,
			Scopes:       body.Generic.Scopes,
			ClaimMapping: body.Generic.ClaimMapping,
		}
		if cfg.ClientID == "" || cfg.IssuerURI == "" {
			stored, err := h.appSettings.GetGenericOIDC(ctx)
			if err != nil {
				writeServerError(c, "oidc test generic", err)
				return
			}
			cfg = stored
		}
		c.JSON(http.StatusOK, h.oidc.TestGeneric(ctx, cfg))

	default:
		writeError(c, http.StatusNotFound, "unknown provider")
	}
}

func presetToDTO(c repo.OAuthPresetConfig) oauthPresetDTO {
	return oauthPresetDTO{
		Enabled:         c.Enabled,
		ClientID:        c.ClientID,
		ClientSecretSet: c.ClientSecret != "",
	}
}

func genericToDTO(c repo.GenericOIDCConfig) genericOIDCDTO {
	return genericOIDCDTO{
		Enabled:         c.Enabled,
		ProviderName:    c.ProviderName,
		ClientID:        c.ClientID,
		ClientSecretSet: c.ClientSecret != "",
		IssuerURI:       c.IssuerURI,
		Scopes:          c.Scopes,
		ClaimMapping:    c.ClaimMapping,
	}
}

// buildRedirectURI derives the OIDC redirect URI from the current
// request when APP_URL is not configured. Browsers compare this exact
// string against the provider's registered list, so both ends must
// agree on scheme/host.
func buildRedirectURI(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := c.Request.Host
	return scheme + "://" + host + "/api/v1/auth/oidc/callback"
}
