package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings keys. Each built-in integration has its own key so admins
// can configure Google, GitHub, and a custom OIDC provider in parallel
// and toggle them independently. Shared knobs (force-only mode,
// auto-provisioning policy) live at the top level.
const (
	SettingOIDCGeneric          = "OIDC_GENERIC"
	SettingOIDCGoogle           = "OIDC_GOOGLE"
	SettingOIDCGitHub           = "OIDC_GITHUB"
	SettingOIDCAutoProvision    = "OIDC_AUTO_PROVISION_DETAILS"
	SettingOIDCForceOnlyMode    = "OIDC_FORCE_ONLY_MODE"
	SettingDefaultNamingPattern = "DEFAULT_FILE_NAMING_PATTERN"
)

// DefaultFileNamingPatternValue is the pattern used when neither the
// library nor the instance admin set one. Keeps a reasonable catalog
// shape out of the box; admins can override at /settings → File
// Naming Patterns.
const DefaultFileNamingPatternValue = "{title}"

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
	pool *pgxpool.Pool
}

func NewAppSettingsRepo(pool *pgxpool.Pool) *AppSettingsRepo {
	return &AppSettingsRepo{pool: pool}
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
	}
}

// -------- generic access --------

// GetRaw returns the JSONB bytes for one setting. Returns ErrNotFound
// when the row is missing — callers decide whether a missing row is
// fatal or a "use default" signal.
func (r *AppSettingsRepo) GetRaw(ctx context.Context, name string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE name = $1`, name).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return raw, nil
}

// SetRaw upserts one setting. Validation is the caller's problem.
func (r *AppSettingsRepo) SetRaw(ctx context.Context, name string, value json.RawMessage) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO app_settings (name, value, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (name) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
	`, name, string(value))
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

// GetDefaultNamingPattern returns the instance-wide fallback pattern.
// Empty string means "keep the original filename" on approval. Callers
// use GetDefaultNamingPatternOr when they want the hardcoded default
// to apply when the row is still empty.
func (r *AppSettingsRepo) GetDefaultNamingPattern(ctx context.Context) (string, error) {
	raw, err := r.GetRaw(ctx, SettingDefaultNamingPattern)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	return v, nil
}

// SetDefaultNamingPattern upserts the instance-wide pattern. Passing
// an empty string stores "" (explicit "no pattern", which makes
// fallback keep the original filename).
func (r *AppSettingsRepo) SetDefaultNamingPattern(ctx context.Context, pattern string) error {
	pattern = strings.TrimSpace(pattern)
	b, err := json.Marshal(pattern)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingDefaultNamingPattern, b)
}

// SeedDefaultNamingPatternIfAbsent writes the built-in pattern on
// first boot so the admin settings UI has something to show. Returning
// the error lets startup proceed; a missing row is non-fatal.
func (r *AppSettingsRepo) SeedDefaultNamingPatternIfAbsent(ctx context.Context) error {
	if _, err := r.GetRaw(ctx, SettingDefaultNamingPattern); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	b, err := json.Marshal(DefaultFileNamingPatternValue)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingDefaultNamingPattern, b)
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
