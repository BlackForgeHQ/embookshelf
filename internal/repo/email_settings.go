package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/crypto"
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

// GetEmail loads the EMAIL row, decrypts the SMTP password through
// cipher, and returns a struct safe to hand to Notifier / Sender. A
// missing row yields DefaultEmailConfig() and a nil error so first
// boot works without a seed migration.
func (r *AppSettingsRepo) GetEmail(ctx context.Context, cipher crypto.Cipher) (EmailConfig, error) {
	raw, err := r.GetRaw(ctx, SettingEmail)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DefaultEmailConfig(), nil
		}
		return EmailConfig{}, err
	}
	cfg := DefaultEmailConfig()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return EmailConfig{}, fmt.Errorf("unmarshal email config: %w", err)
	}
	if cfg.SMTP.Password != "" {
		plain, err := cipher.Decrypt(cfg.SMTP.Password)
		if err != nil {
			return EmailConfig{}, fmt.Errorf("decrypt smtp password: %w", err)
		}
		cfg.SMTP.Password = plain
	}
	return cfg, nil
}

// SetEmail validates the config, encrypts SMTP.Password via cipher,
// and upserts the EMAIL row. The plaintext password is never written
// to the row — callers may pass a sentinel empty string to clear it
// or the same plaintext to refresh.
func (r *AppSettingsRepo) SetEmail(ctx context.Context, cipher crypto.Cipher, cfg EmailConfig) error {
	cfg.SMTP.Host = strings.TrimSpace(cfg.SMTP.Host)
	cfg.SMTP.Username = strings.TrimSpace(cfg.SMTP.Username)
	cfg.From.Address = strings.TrimSpace(cfg.From.Address)
	cfg.From.Name = strings.TrimSpace(cfg.From.Name)
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
	if cfg.SMTP.TLS == "" {
		cfg.SMTP.TLS = "starttls"
	}
	if cfg.SMTP.Password != "" {
		ct, err := cipher.Encrypt(cfg.SMTP.Password)
		if err != nil {
			return fmt.Errorf("encrypt smtp password: %w", err)
		}
		cfg.SMTP.Password = ct
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingEmail, b)
}

// SeedEmailIfAbsent writes a default empty/disabled EMAIL row when
// none exists. Mirrors SeedOIDCIfAbsent so first-boot has a row to
// edit in the settings UI.
func (r *AppSettingsRepo) SeedEmailIfAbsent(ctx context.Context) error {
	if _, err := r.GetRaw(ctx, SettingEmail); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	b, err := json.Marshal(DefaultEmailConfig())
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, SettingEmail, b)
}
