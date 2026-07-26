// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"strings"
)

// SettingEmail keys the EMAIL row in app_settings. Stores a single
// JSON object covering SMTP, From, public URL, and the enabled flag.
// One row, one save — admins edit the email subsystem as a unit. ADR-0020.
const SettingEmail = "EMAIL"

// EmailConfig is the in-memory shape of the EMAIL app_settings row.
// SMTP.Password is plaintext at this struct boundary; the cipher seam
// lives in GetEmail / SetEmail. Callers (handlers, Notifier) never
// see ciphertext.
type EmailConfig struct {
	Enabled   bool            `json:"enabled"`
	SMTP      EmailSMTPConfig `json:"smtp"`
	From      EmailFromConfig `json:"from"`
	PublicURL string          `json:"publicUrl"`
}

// EmailSMTPConfig is the SMTP transport piece. TLS is one of "none",
// "starttls", or "tls" (matching email.TLSMode). Password is
// AES-GCM-encrypted in place when read from / written to the
// database.
type EmailSMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLS      string `json:"tls"`
}

// EmailFromConfig is the visible sender. Address is RFC 5322
// validated at handler save time; Name is free text shown in the
// recipient's inbox.
type EmailFromConfig struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// DefaultEmailConfig is the seed shape for first-boot — everything
// blank and disabled. Admins fill the row out before any email
// feature flips on.
func DefaultEmailConfig() EmailConfig {
	return EmailConfig{
		Enabled: false,
		SMTP: EmailSMTPConfig{
			Port: 587,
			TLS:  "starttls",
		},
	}
}

// emailSetting declares the EMAIL row. SMTP.Password is the one secret;
// host, username, and From stay plaintext so the row reads cleanly in
// psql.
var emailSetting = Setting[EmailConfig]{
	Key:     SettingEmail,
	Default: DefaultEmailConfig,
	Normalize: func(cfg EmailConfig) EmailConfig {
		cfg.SMTP.Host = strings.TrimSpace(cfg.SMTP.Host)
		cfg.SMTP.Username = strings.TrimSpace(cfg.SMTP.Username)
		cfg.From.Address = strings.TrimSpace(cfg.From.Address)
		cfg.From.Name = strings.TrimSpace(cfg.From.Name)
		cfg.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
		if cfg.SMTP.TLS == "" {
			cfg.SMTP.TLS = "starttls"
		}
		return cfg
	},
	Secrets: func(cfg *EmailConfig) []*string { return []*string{&cfg.SMTP.Password} },
}

// GetEmail loads the EMAIL row with the SMTP password decrypted — a
// struct safe to hand to Notifier / Sender. A missing row yields
// DefaultEmailConfig() and a nil error so first boot works without a
// seed migration.
func (r *AppSettingsRepo) GetEmail(ctx context.Context) (EmailConfig, error) {
	return emailSetting.Get(ctx, r)
}

// SetEmail normalizes the config, encrypts SMTP.Password, and upserts
// the EMAIL row. Callers pass an empty password to clear it or the
// same plaintext to refresh.
func (r *AppSettingsRepo) SetEmail(ctx context.Context, cfg EmailConfig) error {
	return emailSetting.Set(ctx, r, cfg)
}

// SeedEmailIfAbsent writes a default empty/disabled EMAIL row when
// none exists, so first boot has a row to edit in the settings UI.
func (r *AppSettingsRepo) SeedEmailIfAbsent(ctx context.Context) error {
	return emailSetting.SeedIfAbsent(ctx, r)
}
