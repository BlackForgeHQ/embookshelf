// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
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
// validated by the row on save; Name is free text shown in the
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

// ErrEmailInvalid marks a config the EMAIL row refuses. Every
// validation failure wraps it and carries its own message, so a handler
// can answer 400 with that message while a cipher or database failure
// under SetEmail still surfaces as the 500 it is. Never returned bare.
var ErrEmailInvalid = errors.New("email")

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
	Validate: func(cfg EmailConfig) error {
		// Completeness only on enable: an admin fills the form in stages,
		// and refusing a half-filled disabled row makes the panel
		// unusable. Enabling one that cannot dial, though, arms password
		// resets and share mails that fail on every send.
		if !cfg.Enabled {
			return nil
		}
		if cfg.SMTP.Host == "" {
			return fmt.Errorf("%w: smtp host is required", ErrEmailInvalid)
		}
		if cfg.SMTP.Port <= 0 || cfg.SMTP.Port > 65535 {
			return fmt.Errorf("%w: smtp port must be 1..65535", ErrEmailInvalid)
		}
		// Normalize has already turned an empty mode into starttls, so
		// only a value the sender cannot interpret reaches this switch.
		switch cfg.SMTP.TLS {
		case "none", "starttls", "tls":
		default:
			return fmt.Errorf("%w: smtp tls must be one of: none, starttls, tls", ErrEmailInvalid)
		}
		if cfg.From.Address == "" {
			return fmt.Errorf("%w: from address is required", ErrEmailInvalid)
		}
		if _, err := mail.ParseAddress(cfg.From.Address); err != nil {
			return fmt.Errorf("%w: from address is not a valid mailbox", ErrEmailInvalid)
		}
		if cfg.PublicURL == "" {
			return fmt.Errorf("%w: public url is required when email is enabled", ErrEmailInvalid)
		}
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: public url must be an absolute http(s) URL", ErrEmailInvalid)
		}
		return nil
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
