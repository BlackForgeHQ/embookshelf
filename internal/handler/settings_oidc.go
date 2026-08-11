// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"io"
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

// oidcSettingsRows is the OIDC surface's aggregate config: the five
// rows one panel edits and one service call applies. It exists so the
// surface fits the settingsDomain pipeline — get reads the rows, save
// hands them to service.OIDCSettingsService.Apply as one transactional
// decision (#195), and the tri-state client secrets ride the same
// declared slots as every other panel's credentials (ADR-0010).
type oidcSettingsRows struct {
	ForceOnly bool
	Auto      repo.OIDCAutoProvisionDetails
	Google    repo.OAuthPresetConfig
	GitHub    repo.OAuthPresetConfig
	Generic   repo.GenericOIDCConfig
}

// oidcSettings declares the OIDC surface. The one shape the panel
// round-trips covers three providers, so the declaration carries three
// secret slots; ClientSecret is preserved when absent — an admin
// editing the client ID shouldn't have to re-enter the secret.
var oidcSettings = settingsDomain[oidcSettingsRows, oidcSettingsDTO]{
	name: "oidc settings",
	// Writes go through the settings service, so the whole surface —
	// including its GET, which production never wires without the
	// service — degrades together.
	ready: func(h *Handler) bool { return h.oidcSettings != nil },
	get: func(ctx context.Context, h *Handler) (oidcSettingsRows, error) {
		var (
			rows oidcSettingsRows
			err  error
		)
		if rows.ForceOnly, err = h.appSettings.GetBool(ctx, repo.SettingOIDCForceOnlyMode); err != nil {
			return rows, err
		}
		if rows.Auto, err = h.appSettings.GetOIDCAutoProvision(ctx); err != nil {
			return rows, err
		}
		if rows.Google, err = h.appSettings.GetGoogle(ctx); err != nil {
			return rows, err
		}
		if rows.GitHub, err = h.appSettings.GetGitHub(ctx); err != nil {
			return rows, err
		}
		rows.Generic, err = h.appSettings.GetGenericOIDC(ctx)
		return rows, err
	},
	save: func(ctx context.Context, h *Handler, rows oidcSettingsRows) error {
		// What the service receives is already-resolved plaintext
		// (ADR-0010); the adapter's secret loop ran before this.
		if err := h.oidcSettings.Apply(ctx, service.OIDCSubmission{
			Google:        rows.Google,
			GitHub:        rows.GitHub,
			Generic:       rows.Generic,
			AutoProvision: rows.Auto,
			ForceOnly:     rows.ForceOnly,
		}); err != nil {
			return err
		}
		// Invalidation belongs to the save, not the response: once the
		// rows changed, the cached provider clients are stale even if
		// the reload that builds the response fails.
		if h.oidc != nil {
			h.oidc.Invalidate()
		}
		return nil
	},
	toDTO: func(h *Handler, c *gin.Context, rows oidcSettingsRows) oidcSettingsDTO {
		return oidcSettingsDTO{
			ForceOnly:     rows.ForceOnly,
			AutoProvision: rows.Auto,
			Google:        presetToDTO(rows.Google),
			GitHub:        presetToDTO(rows.GitHub),
			Generic:       genericToDTO(rows.Generic),
			RedirectURI:   h.buildRedirectURI(c),
		}
	},
	merge: func(dto oidcSettingsDTO, _ oidcSettingsRows) oidcSettingsRows {
		return oidcSettingsRows{
			ForceOnly: dto.ForceOnly,
			Auto:      dto.AutoProvision,
			Google: repo.OAuthPresetConfig{
				Enabled:  dto.Google.Enabled,
				ClientID: strings.TrimSpace(dto.Google.ClientID),
			},
			GitHub: repo.OAuthPresetConfig{
				Enabled:  dto.GitHub.Enabled,
				ClientID: strings.TrimSpace(dto.GitHub.ClientID),
			},
			Generic: repo.GenericOIDCConfig{
				Enabled:      dto.Generic.Enabled,
				ProviderName: strings.TrimSpace(dto.Generic.ProviderName),
				ClientID:     strings.TrimSpace(dto.Generic.ClientID),
				IssuerURI:    strings.TrimSpace(dto.Generic.IssuerURI),
				Scopes:       strings.TrimSpace(dto.Generic.Scopes),
				// Defaults are the Setting declaration's, so a row written by
				// anything other than this endpoint gets them too (#195).
				ClaimMapping: dto.Generic.ClaimMapping,
			},
		}
	},
	secrets: func(dto *oidcSettingsDTO, next, current *oidcSettingsRows) []settingsSecret {
		return []settingsSecret{
			{
				incoming: dto.Google.ClientSecret,
				set:      dto.Google.ClientSecretSet,
				stored:   current.Google.ClientSecret,
				slot:     &next.Google.ClientSecret,
			},
			{
				incoming: dto.GitHub.ClientSecret,
				set:      dto.GitHub.ClientSecretSet,
				stored:   current.GitHub.ClientSecret,
				slot:     &next.GitHub.ClientSecret,
			},
			{
				incoming: dto.Generic.ClientSecret,
				set:      dto.Generic.ClientSecretSet,
				stored:   current.Generic.ClientSecret,
				slot:     &next.Generic.ClientSecret,
			},
		}
	},
	badRequest: func(err error) bool {
		return errIsOneOf(err, service.ErrOIDCIncomplete, service.ErrOIDCForceOnlyBlocked)
	},
}

// SettingsOIDCGet returns everything the OIDC settings page needs.
func (h *Handler) SettingsOIDCGet(c *gin.Context) { settingsGet(c, h, oidcSettings) }

// SettingsOIDCUpdate accepts the full DTO and saves each provider's
// row plus the shared flags.
func (h *Handler) SettingsOIDCUpdate(c *gin.Context) { settingsPut(c, h, oidcSettings) }

// SettingsOIDCTest runs the connection diagnostic for one provider at a
// time. The slug is supplied in the URL; the body carries an optional
// override config (useful when testing before saving). The service owns
// the provider registry, the override shapes, and the blank-submission
// fallback, so this endpoint is a lookup plus one call (#258).
func (h *Handler) SettingsOIDCTest(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusServiceUnavailable, "oidc service unavailable")
		return
	}
	slug := c.Param("slug")
	body, _ := io.ReadAll(c.Request.Body)
	res, err := h.oidc.TestProvider(c.Request.Context(), slug, body)
	switch {
	case errors.Is(err, service.ErrOIDCUnknownProvider):
		writeError(c, http.StatusNotFound, "unknown provider")
	case err != nil:
		writeServerError(c, "oidc test "+slug, err)
	default:
		c.JSON(http.StatusOK, res)
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

// buildRedirectURI derives the OIDC redirect URI the admin has to
// register at the provider. Providers compare this exact string against
// their registered list, so what the panel shows has to be what the
// login flow sends — it mirrors service.resolveRedirectURL: APP_URL
// when configured, the shared requestOrigin otherwise. Reading the
// proxy headers a second time here is what made the two disagree.
func (h *Handler) buildRedirectURI(c *gin.Context) string {
	base := strings.TrimRight(h.cfg.AppURL, "/")
	if base == "" {
		base = requestOrigin(c)
	}
	if base == "" {
		return ""
	}
	return base + "/api/v1/auth/oidc/callback"
}
