// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// SettingConverter names the app_settings row configuring the converter
// extension (ADR-0033): the sidecar that turns Convertible-format books
// into Markdown renditions.
const SettingConverter = "CONVERTER"

// ConverterConfig is the CONVERTER row. No secrets in v1 — the sidecar
// binds to the internal network; a bearer token joins here (via a
// Secrets declaration) if the sidecar is ever exposed beyond it.
type ConverterConfig struct {
	// Enabled gates the extension. Off by default — most installs never
	// deploy the sidecar, and features that need it report "extension
	// not configured" rather than dialing a URL nobody set.
	Enabled bool `json:"enabled"`
	// BaseURL is where the sidecar listens, e.g. http://converter:6070.
	// The server dials {BaseURL}/convert and {BaseURL}/healthz.
	BaseURL string `json:"baseUrl"`
}

func DefaultConverterConfig() ConverterConfig {
	return ConverterConfig{}
}

var converterSetting = Setting[ConverterConfig]{
	Key:     SettingConverter,
	Default: DefaultConverterConfig,
	Normalize: func(cfg ConverterConfig) ConverterConfig {
		// Operators paste endpoints with a trailing slash; "/convert"
		// appended would produce a double slash.
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		return cfg
	},
	Validate: func(cfg ConverterConfig) error {
		// Completeness-on-enable only: a half-filled disabled row is an
		// admin mid-edit, not an error.
		if !cfg.Enabled {
			return nil
		}
		if cfg.BaseURL == "" {
			return errors.New("the converter needs a base URL before it can be enabled")
		}
		u, err := url.Parse(cfg.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("converter base URL %q is not an http(s) URL", cfg.BaseURL)
		}
		return nil
	},
}

// GetConverter loads the row. A missing row yields defaults and a nil
// error — "never configured" and "configured off" read identically.
func (r *AppSettingsRepo) GetConverter(ctx context.Context) (ConverterConfig, error) {
	return converterSetting.Get(ctx, r)
}

// SetConverter normalizes, validates and upserts.
func (r *AppSettingsRepo) SetConverter(ctx context.Context, cfg ConverterConfig) error {
	return converterSetting.Set(ctx, r, cfg)
}
