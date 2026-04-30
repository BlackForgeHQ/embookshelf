package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
)

// Settings keys. Each built-in integration has its own key so admins
// can configure Google, GitHub, and a custom OIDC provider in parallel
// and toggle them independently. Shared knobs (force-only mode,
// auto-provisioning policy) live at the top level.
const (
	SettingOIDCGeneric       = "OIDC_GENERIC"
	SettingOIDCGoogle        = "OIDC_GOOGLE"
	SettingOIDCGitHub        = "OIDC_GITHUB"
	SettingOIDCAutoProvision = "OIDC_AUTO_PROVISION_DETAILS"
	SettingOIDCForceOnlyMode = "OIDC_FORCE_ONLY_MODE"
	// SettingMetadataAutoEnrich, when true, triggers the enrichment
	// service on bookdrop approval so newly imported books land with
	// provider metadata already applied. Default false — opt-in.
	SettingMetadataAutoEnrich = "METADATA_AUTO_ENRICH"
)

// Provider slugs used on the wire (URL path, state cache, login page).
const (
	ProviderSlugGeneric = "generic"
	ProviderSlugGoogle  = "google"
	ProviderSlugGitHub  = "github"
)

// AppSettingsRepo stores instance-wide configuration that admins can edit
// from the UI at runtime. Values are JSONB so scalars, arrays, and objects
// can share the one table.
type AppSettingsRepo struct {
	db *db.DB
}

func NewAppSettingsRepo(d *db.DB) *AppSettingsRepo {
	return &AppSettingsRepo{db: d}
}

// ClaimMapping names the ID-token / userinfo claims we read for each
// profile field. Only the generic OIDC provider exposes this — Google
// and GitHub use fixed claims baked into the service.
type ClaimMapping struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Groups   string `json:"groups,omitempty"`
}

// GenericOIDCConfig is the full OIDC config surface — issuer URL, scopes,
// and claim mapping. Used when an admin wires up Authentik / Keycloak /
// Authelia / etc.
type GenericOIDCConfig struct {
	Enabled      bool         `json:"enabled"`
	ProviderName string       `json:"providerName"`
	ClientID     string       `json:"clientId"`
	ClientSecret string       `json:"clientSecret,omitempty"`
	IssuerURI    string       `json:"issuerUri"`
	Scopes       string       `json:"scopes"`
	ClaimMapping ClaimMapping `json:"claimMapping"`
}

// OAuthPresetConfig is the tiny config surface used for built-in
// providers whose endpoints/scopes/claim mapping are hard-coded in the
// service (Google, GitHub). The admin only supplies credentials.
type OAuthPresetConfig struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// OIDCAutoProvisionDetails mirrors section 4.2. DefaultRole is this
// codebase's simpler analogue of "default permissions + libraries" in
// the spec — our permission model is admin/user, not per-library.
type OIDCAutoProvisionDetails struct {
	EnableAutoProvisioning   bool   `json:"enableAutoProvisioning"`
	AllowLocalAccountLinking bool   `json:"allowLocalAccountLinking"`
	DefaultRole              string `json:"defaultRole"` // "admin" | "user"
	RequireAdminApproval     bool   `json:"requireAdminApproval"`
}

// -------- defaults --------

func DefaultGenericOIDCConfig() GenericOIDCConfig {
	return GenericOIDCConfig{
		Scopes: "openid profile email",
		ClaimMapping: ClaimMapping{
			Username: "preferred_username",
			Email:    "email",
			Name:     "name",
			Groups:   "groups",
		},
	}
}

func DefaultOAuthPresetConfig() OAuthPresetConfig { return OAuthPresetConfig{} }

func DefaultOIDCAutoProvisionDetails() OIDCAutoProvisionDetails {
	return OIDCAutoProvisionDetails{
		EnableAutoProvisioning:   false,
		AllowLocalAccountLinking: true,
		DefaultRole:              "user",
		RequireAdminApproval:     false,
	}
}

// -------- generic access --------

// GetRaw returns the JSONB bytes for one setting. Returns ErrNotFound
// when the row is missing — callers decide whether a missing row is
// fatal or a "use default" signal.
func (r *AppSettingsRepo) GetRaw(ctx context.Context, name string) (json.RawMessage, error) {
	const qPG = `SELECT value FROM app_settings WHERE name = $1`
	const qSQLite = `SELECT value FROM app_settings WHERE name = ?`
	var raw []byte
	err := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), name).Scan(&raw)
	if err != nil {
		if dberr.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// SetRaw upserts one setting. Validation is the caller's problem.
func (r *AppSettingsRepo) SetRaw(ctx context.Context, name string, value json.RawMessage) error {
	const qPG = `
		INSERT INTO app_settings (name, value, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (name) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
	`
	const qSQLite = `
		INSERT INTO app_settings (name, value, updated_at)
		VALUES (?, ?, (strftime('%Y-%m-%dT%H:%M:%fZ','now')))
		ON CONFLICT (name) DO UPDATE
		SET value = EXCLUDED.value, updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	`
	_, err := r.db.SQL.ExecContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		name, string(value))
	return err
}

// GetBool returns (false, nil) when the row is missing so callers don't
// need to distinguish between "never set" and "explicitly false" for
// the common toggle case.
func (r *AppSettingsRepo) GetBool(ctx context.Context, name string) (bool, error) {
	raw, err := r.GetRaw(ctx, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, err
	}
	return v, nil
}

func (r *AppSettingsRepo) SetBool(ctx context.Context, name string, v bool) error {
	b, _ := json.Marshal(v)
	return r.SetRaw(ctx, name, b)
}

// -------- per-provider accessors --------

func (r *AppSettingsRepo) GetGenericOIDC(ctx context.Context) (GenericOIDCConfig, error) {
	raw, err := r.GetRaw(ctx, SettingOIDCGeneric)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DefaultGenericOIDCConfig(), nil
		}
		return GenericOIDCConfig{}, err
	}
	c := DefaultGenericOIDCConfig()
	if err := json.Unmarshal(raw, &c); err != nil {
		return GenericOIDCConfig{}, err
	}
	return c, nil
}

func (r *AppSettingsRepo) SetGenericOIDC(ctx context.Context, c GenericOIDCConfig) error {
	c.ProviderName = strings.TrimSpace(c.ProviderName)
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.IssuerURI = strings.TrimRight(strings.TrimSpace(c.IssuerURI), "/")
	c.Scopes = strings.TrimSpace(c.Scopes)
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingOIDCGeneric, b)
}

func (r *AppSettingsRepo) GetGoogle(ctx context.Context) (OAuthPresetConfig, error) {
	return r.getPreset(ctx, SettingOIDCGoogle)
}

func (r *AppSettingsRepo) SetGoogle(ctx context.Context, c OAuthPresetConfig) error {
	return r.setPreset(ctx, SettingOIDCGoogle, c)
}

func (r *AppSettingsRepo) GetGitHub(ctx context.Context) (OAuthPresetConfig, error) {
	return r.getPreset(ctx, SettingOIDCGitHub)
}

func (r *AppSettingsRepo) SetGitHub(ctx context.Context, c OAuthPresetConfig) error {
	return r.setPreset(ctx, SettingOIDCGitHub, c)
}

func (r *AppSettingsRepo) getPreset(ctx context.Context, name string) (OAuthPresetConfig, error) {
	raw, err := r.GetRaw(ctx, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DefaultOAuthPresetConfig(), nil
		}
		return OAuthPresetConfig{}, err
	}
	var c OAuthPresetConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return OAuthPresetConfig{}, err
	}
	return c, nil
}

func (r *AppSettingsRepo) setPreset(ctx context.Context, name string, c OAuthPresetConfig) error {
	c.ClientID = strings.TrimSpace(c.ClientID)
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, name, b)
}

func (r *AppSettingsRepo) GetOIDCAutoProvision(ctx context.Context) (OIDCAutoProvisionDetails, error) {
	raw, err := r.GetRaw(ctx, SettingOIDCAutoProvision)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DefaultOIDCAutoProvisionDetails(), nil
		}
		return OIDCAutoProvisionDetails{}, err
	}
	ap := DefaultOIDCAutoProvisionDetails()
	if err := json.Unmarshal(raw, &ap); err != nil {
		return OIDCAutoProvisionDetails{}, err
	}
	return ap, nil
}

func (r *AppSettingsRepo) SetOIDCAutoProvision(ctx context.Context, ap OIDCAutoProvisionDetails) error {
	if ap.DefaultRole != "admin" && ap.DefaultRole != "user" {
		ap.DefaultRole = "user"
	}
	b, err := json.Marshal(ap)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingOIDCAutoProvision, b)
}

// SeedOIDCIfAbsent writes defaults for any OIDC setting still missing
// so the admin settings UI has sensible rows to render on first boot.
func (r *AppSettingsRepo) SeedOIDCIfAbsent(ctx context.Context) error {
	seed := func(key string, defaultValue any) error {
		if _, err := r.GetRaw(ctx, key); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		b, err := json.Marshal(defaultValue)
		if err != nil {
			return err
		}
		return r.SetRaw(ctx, key, b)
	}
	if err := seed(SettingOIDCGeneric, DefaultGenericOIDCConfig()); err != nil {
		return err
	}
	if err := seed(SettingOIDCGoogle, DefaultOAuthPresetConfig()); err != nil {
		return err
	}
	if err := seed(SettingOIDCGitHub, DefaultOAuthPresetConfig()); err != nil {
		return err
	}
	if err := seed(SettingOIDCAutoProvision, DefaultOIDCAutoProvisionDetails()); err != nil {
		return err
	}
	if err := seed(SettingOIDCForceOnlyMode, false); err != nil {
		return err
	}
	return nil
}
