// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/crypto"
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
//
// The repo holds the Cipher rather than taking it per call: secret
// handling is a property of the row, declared once on its Setting, so
// no accessor can be added that silently stores a secret in plaintext.
type AppSettingsRepo struct {
	db     *db.DB
	cipher crypto.Cipher
}

func NewAppSettingsRepo(d *db.DB, cipher crypto.Cipher) *AppSettingsRepo {
	if cipher == nil {
		cipher = crypto.Noop{}
	}
	return &AppSettingsRepo{db: d, cipher: cipher}
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
	const q = `SELECT value FROM app_settings WHERE name = $1`
	var raw []byte
	err := r.db.SQL.QueryRowContext(ctx, q, name).Scan(&raw)
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
	const q = `
		INSERT INTO app_settings (name, value, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (name) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
	`
	_, err := r.db.SQL.ExecContext(ctx, q, name, string(value))
	return err
}

// SetRows upserts several settings in one transaction.
//
// All or nothing, which is the point: a submission that configures three
// providers, an auto-provision policy and a force-only flag is one
// decision, and landing part of it leaves an instance in a state nobody
// asked for — including, before this, one where the lockout guard
// refused the last row after the first four had already been written
// (#195).
func (r *AppSettingsRepo) SetRows(ctx context.Context, rows []SettingRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		INSERT INTO app_settings (name, value, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (name) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
	`
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx, q, row.Name, string(row.Value)); err != nil {
			return fmt.Errorf("write setting %s: %w", row.Name, err)
		}
	}
	return tx.Commit()
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

// -------- per-provider settings --------

// genericOIDCSetting declares the custom-OIDC row. ClientSecret is the
// only secret; the rest stays plaintext so the row is legible in psql.
var genericOIDCSetting = Setting[GenericOIDCConfig]{
	Key:     SettingOIDCGeneric,
	Default: DefaultGenericOIDCConfig,
	// The defaults are here rather than at the write site, so a reader
	// that never went through the settings endpoint gets them too — the
	// login flow reading a row an operator seeded by hand, say. They used
	// to live in the HTTP handler, which meant an empty scope list was
	// only ever filled in for callers who arrived over HTTP (#195).
	Normalize: func(c GenericOIDCConfig) GenericOIDCConfig {
		c.ProviderName = strings.TrimSpace(c.ProviderName)
		c.ClientID = strings.TrimSpace(c.ClientID)
		c.IssuerURI = strings.TrimRight(strings.TrimSpace(c.IssuerURI), "/")
		c.Scopes = strings.TrimSpace(c.Scopes)

		d := DefaultGenericOIDCConfig()
		if c.Scopes == "" {
			c.Scopes = d.Scopes
		}
		// Claim mapping is three independent fields: a submission that
		// names one and leaves the others blank should get the defaults
		// for the two it did not mention, not a login that reads an
		// empty claim name.
		if c.ClaimMapping.Username == "" {
			c.ClaimMapping.Username = d.ClaimMapping.Username
		}
		if c.ClaimMapping.Email == "" {
			c.ClaimMapping.Email = d.ClaimMapping.Email
		}
		if c.ClaimMapping.Name == "" {
			c.ClaimMapping.Name = d.ClaimMapping.Name
		}
		return c
	},
	Secrets: func(c *GenericOIDCConfig) []*string { return []*string{&c.ClientSecret} },
}

// presetSetting builds the declaration for a built-in provider row
// (Google, GitHub) — same shape, different key.
func presetSetting(key string) Setting[OAuthPresetConfig] {
	return Setting[OAuthPresetConfig]{
		Key:     key,
		Default: DefaultOAuthPresetConfig,
		Normalize: func(c OAuthPresetConfig) OAuthPresetConfig {
			c.ClientID = strings.TrimSpace(c.ClientID)
			return c
		},
		Secrets: func(c *OAuthPresetConfig) []*string { return []*string{&c.ClientSecret} },
	}
}

var (
	googleSetting = presetSetting(SettingOIDCGoogle)
	githubSetting = presetSetting(SettingOIDCGitHub)

	autoProvisionSetting = Setting[OIDCAutoProvisionDetails]{
		Key:     SettingOIDCAutoProvision,
		Default: DefaultOIDCAutoProvisionDetails,
		Normalize: func(ap OIDCAutoProvisionDetails) OIDCAutoProvisionDetails {
			if ap.DefaultRole != "admin" && ap.DefaultRole != "user" {
				ap.DefaultRole = "user"
			}
			return ap
		},
	}

	// forceOnlyModeSetting is the one OIDC row whose value is a bare
	// bool. Declared rather than hand-marshaled so it seeds and prepares
	// through the same mechanism as its four neighbours; GetBool /
	// SetBool remain the read path for callers that only want the flag.
	forceOnlyModeSetting = Setting[bool]{
		Key:     SettingOIDCForceOnlyMode,
		Default: func() bool { return false },
	}
)

func (r *AppSettingsRepo) GetGenericOIDC(ctx context.Context) (GenericOIDCConfig, error) {
	return genericOIDCSetting.Get(ctx, r)
}

func (r *AppSettingsRepo) SetGenericOIDC(ctx context.Context, c GenericOIDCConfig) error {
	return genericOIDCSetting.Set(ctx, r, c)
}

func (r *AppSettingsRepo) GetGoogle(ctx context.Context) (OAuthPresetConfig, error) {
	return googleSetting.Get(ctx, r)
}

func (r *AppSettingsRepo) SetGoogle(ctx context.Context, c OAuthPresetConfig) error {
	return googleSetting.Set(ctx, r, c)
}

func (r *AppSettingsRepo) GetGitHub(ctx context.Context) (OAuthPresetConfig, error) {
	return githubSetting.Get(ctx, r)
}

func (r *AppSettingsRepo) SetGitHub(ctx context.Context, c OAuthPresetConfig) error {
	return githubSetting.Set(ctx, r, c)
}

func (r *AppSettingsRepo) GetOIDCAutoProvision(ctx context.Context) (OIDCAutoProvisionDetails, error) {
	return autoProvisionSetting.Get(ctx, r)
}

func (r *AppSettingsRepo) SetOIDCAutoProvision(ctx context.Context, ap OIDCAutoProvisionDetails) error {
	return autoProvisionSetting.Set(ctx, r, ap)
}

// OIDCRows is one settings submission's five rows, as plaintext values.
// Named here rather than in the service so the marshalling, the
// normalisation and the encryption stay with the Setting declarations
// that own them.
type OIDCRows struct {
	Google        OAuthPresetConfig
	GitHub        OAuthPresetConfig
	Generic       GenericOIDCConfig
	AutoProvision OIDCAutoProvisionDetails
	ForceOnly     bool
}

// PrepareOIDCSettingRows normalizes, validates and encrypts every row a
// submission touches, without writing any of them.
//
// Preparing before writing is what lets the five land in one
// transaction: an encryption failure on the third row now costs the
// whole submission rather than leaving the first two applied (#195).
func (r *AppSettingsRepo) PrepareOIDCSettingRows(in OIDCRows) ([]SettingRow, error) {
	rows := make([]SettingRow, 0, 5)
	google, err := googleSetting.Prepare(r, in.Google)
	if err != nil {
		return nil, err
	}
	github, err := githubSetting.Prepare(r, in.GitHub)
	if err != nil {
		return nil, err
	}
	generic, err := genericOIDCSetting.Prepare(r, in.Generic)
	if err != nil {
		return nil, err
	}
	auto, err := autoProvisionSetting.Prepare(r, in.AutoProvision)
	if err != nil {
		return nil, err
	}
	force, err := forceOnlyModeSetting.Prepare(r, in.ForceOnly)
	if err != nil {
		return nil, err
	}
	rows = append(rows, google, github, generic, auto, force)
	return rows, nil
}

// settingsRegistry is the single list of app_settings rows this binary
// seeds on boot. Adding a settings domain means adding one line here —
// and the parity test in setting_test.go fails until it is added, which
// is the point: an unseeded row reads back as its default, so forgetting
// used to cost nothing at runtime and an empty panel in the admin UI.
//
// Shaped like the job registry: an unexported registration built from
// the declaration it names, taking the one dependency the work needs.
func settingsRegistry(r *AppSettingsRepo) []settingSeeder {
	return []settingSeeder{
		seedRow(r, genericOIDCSetting),
		seedRow(r, googleSetting),
		seedRow(r, githubSetting),
		seedRow(r, autoProvisionSetting),
		seedRow(r, forceOnlyModeSetting),
		seedRow(r, forwardAuthSetting),
		seedRow(r, readingGuideSetting),
		seedRow(r, audiobookSetting),
		seedRow(r, emailSetting),
		seedRow(r, converterSetting),
	}
}

// SeedAll writes the default row for every registered setting that has
// none, so the admin settings UI has something to render on first boot.
// Existing rows — including admin-edited ones — are left alone, so this
// is safe to run on every restart.
//
// Every row is attempted even when one fails, and the failures are
// joined: they are independent rows, and a domain whose seed errored is
// no reason to leave the rest of the instance unconfigured. A returned
// error is not fatal at boot — an unseeded feature runs on its declared
// defaults, the state a fresh install is in anyway.
func (r *AppSettingsRepo) SeedAll(ctx context.Context) error {
	return seedRows(ctx, settingsRegistry(r))
}

// seedRows runs every seed step, keeping going past a failure.
func seedRows(ctx context.Context, rows []settingSeeder) error {
	var errs []error
	for _, row := range rows {
		if err := row.seed(ctx); err != nil {
			errs = append(errs, fmt.Errorf("seed %s setting: %w", row.key, err))
		}
	}
	return errors.Join(errs...)
}
