package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// oidcSettingsDTO is the shape the admin UI exchanges with the server.
// ClientSecret is elided on GET (never leaks to the browser) and is only
// overwritten on PUT when the caller actually supplies a value — empty
// string means "don't touch".
type oidcSettingsDTO struct {
	Enabled       bool                         `json:"enabled"`
	ForceOnly     bool                         `json:"forceOnly"`
	Provider      oidcProviderDTO              `json:"provider"`
	AutoProvision repo.OIDCAutoProvisionDetails `json:"autoProvision"`
	RedirectURI   string                       `json:"redirectUri"`
}

type oidcProviderDTO struct {
	ProviderName       string            `json:"providerName"`
	ClientID           string            `json:"clientId"`
	ClientSecretSet    bool              `json:"clientSecretSet"`
	ClientSecret       string            `json:"clientSecret,omitempty"`
	IssuerURI          string            `json:"issuerUri"`
	Scopes             string            `json:"scopes"`
	ClaimMapping       repo.ClaimMapping `json:"claimMapping"`
}

// SettingsOIDCGet returns the full admin view.
func (h *Handler) SettingsOIDCGet(c *gin.Context) {
	if h.appSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return
	}
	ctx := c.Request.Context()

	enabled, err := h.appSettings.GetBool(ctx, repo.SettingOIDCEnabled)
	if err != nil {
		writeServerError(c, "oidc settings enabled", err)
		return
	}
	force, err := h.appSettings.GetBool(ctx, repo.SettingOIDCForceOnlyMode)
	if err != nil {
		writeServerError(c, "oidc settings force", err)
		return
	}
	provider, err := h.appSettings.GetOIDCProvider(ctx)
	if err != nil {
		writeServerError(c, "oidc settings provider", err)
		return
	}
	auto, err := h.appSettings.GetOIDCAutoProvision(ctx)
	if err != nil {
		writeServerError(c, "oidc settings auto-provision", err)
		return
	}
	redirect := ""
	if h.oidc != nil {
		// Compute via the same path the service uses so docs and wire
		// agree. Minor duplication but the settings page wants an
		// absolute URL even if AppURL is unset (fall back to request).
		redirect = buildRedirectURI(c)
	}

	dto := oidcSettingsDTO{
		Enabled:   enabled,
		ForceOnly: force,
		Provider: oidcProviderDTO{
			ProviderName:    provider.ProviderName,
			ClientID:        provider.ClientID,
			ClientSecretSet: provider.ClientSecret != "",
			IssuerURI:       provider.IssuerURI,
			Scopes:          provider.Scopes,
			ClaimMapping:    provider.ClaimMapping,
		},
		AutoProvision: auto,
		RedirectURI:   redirect,
	}
	c.JSON(http.StatusOK, dto)
}

// SettingsOIDCUpdate accepts a full or partial DTO. ClientSecret is
// preserved when absent — an empty string never wipes an existing secret
// accidentally (set ClientSecretSet=false explicitly to clear).
func (h *Handler) SettingsOIDCUpdate(c *gin.Context) {
	if h.appSettings == nil {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return
	}
	var body oidcSettingsDTO
	if !bindJSON(c, &body) {
		return
	}
	ctx := c.Request.Context()

	// Load existing provider so we can preserve ClientSecret when the
	// UI doesn't send one.
	existing, err := h.appSettings.GetOIDCProvider(ctx)
	if err != nil {
		writeServerError(c, "oidc settings existing", err)
		return
	}

	secret := existing.ClientSecret
	if body.Provider.ClientSecret != "" {
		secret = body.Provider.ClientSecret
	}
	if !body.Provider.ClientSecretSet && body.Provider.ClientSecret == "" {
		// Explicit clear.
		secret = ""
	}

	provider := repo.OIDCProviderDetails{
		ProviderName: strings.TrimSpace(body.Provider.ProviderName),
		ClientID:     strings.TrimSpace(body.Provider.ClientID),
		ClientSecret: secret,
		IssuerURI:    strings.TrimSpace(body.Provider.IssuerURI),
		Scopes:       strings.TrimSpace(body.Provider.Scopes),
		ClaimMapping: body.Provider.ClaimMapping,
	}
	if provider.ClaimMapping.Username == "" {
		provider.ClaimMapping.Username = "preferred_username"
	}
	if provider.ClaimMapping.Email == "" {
		provider.ClaimMapping.Email = "email"
	}
	if provider.ClaimMapping.Name == "" {
		provider.ClaimMapping.Name = "name"
	}
	if err := h.appSettings.SetOIDCProvider(ctx, provider); err != nil {
		writeServerError(c, "oidc save provider", err)
		return
	}

	// Validate enabled transition: refuse to enable without a usable
	// config, but allow the admin to disable at any time.
	if body.Enabled {
		if provider.ClientID == "" || provider.IssuerURI == "" {
			writeError(c, http.StatusBadRequest, "OIDC cannot be enabled without issuerUri and clientId")
			return
		}
	}
	if err := h.appSettings.SetBool(ctx, repo.SettingOIDCEnabled, body.Enabled); err != nil {
		writeServerError(c, "oidc save enabled", err)
		return
	}

	// Auto-provision.
	if err := h.appSettings.SetOIDCAutoProvision(ctx, body.AutoProvision); err != nil {
		writeServerError(c, "oidc save auto-provision", err)
		return
	}

	// Force-only: server-side guard in case the UI didn't check.
	if h.oidc != nil {
		if err := h.oidc.ValidateForceOnlyTransition(ctx, body.ForceOnly); err != nil {
			if errors.Is(err, service.ErrOIDCForceOnlyBlocked) {
				writeError(c, http.StatusBadRequest, err.Error())
				return
			}
			writeServerError(c, "oidc force-only validate", err)
			return
		}
	}
	if err := h.appSettings.SetBool(ctx, repo.SettingOIDCForceOnlyMode, body.ForceOnly); err != nil {
		writeServerError(c, "oidc save force", err)
		return
	}

	// Bust the discovery cache so the next login uses the new settings.
	if h.oidc != nil {
		h.oidc.Invalidate()
	}
	h.SettingsOIDCGet(c)
}

type oidcTestBody struct {
	Provider oidcProviderDTO `json:"provider"`
}

// SettingsOIDCTest runs the diagnostic checks in spec section 6.7. It
// does not mutate any settings — admins use it before saving to catch
// issuer/JWKS issues up front.
func (h *Handler) SettingsOIDCTest(c *gin.Context) {
	if h.oidc == nil {
		writeError(c, http.StatusServiceUnavailable, "oidc service unavailable")
		return
	}
	var body oidcTestBody
	if !bindJSON(c, &body) {
		return
	}
	// Allow the admin to test against either a fresh DTO or — when no
	// provider is supplied — the stored config.
	details := repo.OIDCProviderDetails{
		ProviderName: body.Provider.ProviderName,
		ClientID:     body.Provider.ClientID,
		ClientSecret: body.Provider.ClientSecret,
		IssuerURI:    body.Provider.IssuerURI,
		Scopes:       body.Provider.Scopes,
		ClaimMapping: body.Provider.ClaimMapping,
	}
	if details.IssuerURI == "" || details.ClientID == "" {
		// Fall back to stored values when the admin clicks "Test" on
		// the page before editing anything.
		stored, err := h.appSettings.GetOIDCProvider(c.Request.Context())
		if err != nil {
			writeServerError(c, "oidc test load", err)
			return
		}
		details = stored
	}

	res := h.oidc.TestConnection(c.Request.Context(), details)
	c.JSON(http.StatusOK, res)
}

// buildRedirectURI derives the OIDC redirect URI from the current
// request when AppURL is not configured. Browsers use this exact string
// to compare against the provider's registered list, so both ends must
// agree.
func buildRedirectURI(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := c.Request.Host
	return scheme + "://" + host + "/api/v1/auth/oidc/callback"
}
