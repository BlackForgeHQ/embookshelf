package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// App-settings keys. Kept here so the service layer and handlers both
// reference the same strings — typo drift in this table is hard to debug
// because missing keys look identical to "never set".
const (
	SettingOIDCEnabled         = "OIDC_ENABLED"
	SettingOIDCProvider        = "OIDC_PROVIDER_DETAILS"
	SettingOIDCAutoProvision   = "OIDC_AUTO_PROVISION_DETAILS"
	SettingOIDCForceOnlyMode   = "OIDC_FORCE_ONLY_MODE"
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

// OIDCProviderDetails mirrors section 4.1 of oidc.spec.md. ClientSecret is
// stripped from the public settings response — the repo keeps it; the
// handler decides who sees it.
type OIDCProviderDetails struct {
	ProviderName string       `json:"providerName"`
	ClientID     string       `json:"clientId"`
	ClientSecret string       `json:"clientSecret,omitempty"`
	IssuerURI    string       `json:"issuerUri"`
	Scopes       string       `json:"scopes,omitempty"`
	ClaimMapping ClaimMapping `json:"claimMapping"`
}

// ClaimMapping names the ID-token / userinfo claims we read for each
// profile field. Groups is optional and only consulted when the mapping
// service is wired up (deferred to a later phase).
type ClaimMapping struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Groups   string `json:"groups,omitempty"`
}

// OIDCAutoProvisionDetails mirrors section 4.2. DefaultRole is this
// codebase's simpler analogue of "default permissions + libraries" in the
// spec — our permission model is admin/user, not per-library.
type OIDCAutoProvisionDetails struct {
	EnableAutoProvisioning   bool   `json:"enableAutoProvisioning"`
	AllowLocalAccountLinking bool   `json:"allowLocalAccountLinking"`
	DefaultRole              string `json:"defaultRole"` // "admin" | "user"
}

// Defaults mirrors the form defaults documented in section 4.1.
func DefaultOIDCProviderDetails() OIDCProviderDetails {
	return OIDCProviderDetails{
		Scopes: "openid profile email",
		ClaimMapping: ClaimMapping{
			Username: "preferred_username",
			Email:    "email",
			Name:     "name",
			Groups:   "groups",
		},
	}
}

// Defaults per spec 4.2.
func DefaultOIDCAutoProvisionDetails() OIDCAutoProvisionDetails {
	return OIDCAutoProvisionDetails{
		EnableAutoProvisioning:   false,
		AllowLocalAccountLinking: true,
		DefaultRole:              "user",
	}
}

// GetRaw returns the JSONB bytes for one setting. Returns ErrNotFound when
// the row is missing — callers decide whether a missing row is fatal or a
// "use default" signal.
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
// need to distinguish between "never set" and "explicitly false" for the
// common toggle case.
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

// GetOIDCProvider returns the stored provider config or the documented
// defaults when no row exists yet.
func (r *AppSettingsRepo) GetOIDCProvider(ctx context.Context) (OIDCProviderDetails, error) {
	raw, err := r.GetRaw(ctx, SettingOIDCProvider)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DefaultOIDCProviderDetails(), nil
		}
		return OIDCProviderDetails{}, err
	}
	// Merge stored values onto defaults so partial rows (e.g. missing
	// claim mapping after an older migration) stay usable.
	p := DefaultOIDCProviderDetails()
	if err := json.Unmarshal(raw, &p); err != nil {
		return OIDCProviderDetails{}, err
	}
	return p, nil
}

func (r *AppSettingsRepo) SetOIDCProvider(ctx context.Context, p OIDCProviderDetails) error {
	p.ProviderName = strings.TrimSpace(p.ProviderName)
	p.ClientID = strings.TrimSpace(p.ClientID)
	p.IssuerURI = strings.TrimRight(strings.TrimSpace(p.IssuerURI), "/")
	p.Scopes = strings.TrimSpace(p.Scopes)
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingOIDCProvider, b)
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

// SeedIfAbsent writes the defaults for any OIDC setting that is still
// missing. Invoked at boot so first-time admins see sensible values in
// the settings UI instead of empty fields.
func (r *AppSettingsRepo) SeedOIDCIfAbsent(ctx context.Context) error {
	if _, err := r.GetRaw(ctx, SettingOIDCEnabled); errors.Is(err, ErrNotFound) {
		if err := r.SetBool(ctx, SettingOIDCEnabled, false); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := r.GetRaw(ctx, SettingOIDCProvider); errors.Is(err, ErrNotFound) {
		if err := r.SetOIDCProvider(ctx, DefaultOIDCProviderDetails()); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := r.GetRaw(ctx, SettingOIDCAutoProvision); errors.Is(err, ErrNotFound) {
		if err := r.SetOIDCAutoProvision(ctx, DefaultOIDCAutoProvisionDetails()); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := r.GetRaw(ctx, SettingOIDCForceOnlyMode); errors.Is(err, ErrNotFound) {
		if err := r.SetBool(ctx, SettingOIDCForceOnlyMode, false); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}
