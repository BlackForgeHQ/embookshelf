// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// SettingForwardAuth keys the FORWARD_AUTH row in app_settings. Stores
// the full forward-auth surface as one JSON object — admins edit it
// as a unit. ADR-0022.
const SettingForwardAuth = "FORWARD_AUTH"

// ProviderSlugProxy is the user_identities.provider value for rows
// materialised by the forward-auth middleware. Distinct from the
// three OIDC slugs.
const ProviderSlugProxy = "proxy"

// ForwardAuthHeaders names the request headers the middleware reads
// for each identity field. Operator points these at whatever the
// upstream proxy emits — Authelia (`Remote-*`) or oauth2-proxy
// (`X-Forwarded-*`) being the common cases. ADR-0022.
type ForwardAuthHeaders struct {
	User   string `json:"user"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Groups string `json:"groups,omitempty"`
}

// ForwardAuthConfig is the in-memory shape of the FORWARD_AUTH row.
// All fields are plaintext at this struct boundary — no secrets in
// the forward-auth path (the trust gate is the IP CIDR list, not a
// shared secret). ADR-0022.
type ForwardAuthConfig struct {
	Enabled           bool               `json:"enabled"`
	TrustedProxyCIDRs []string           `json:"trustedProxyCIDRs"`
	Headers           ForwardAuthHeaders `json:"headers"`
	LogoutURL         string             `json:"logoutUrl,omitempty"`
	HideLocalLogin    bool               `json:"hideLocalLogin"`
}

// DefaultForwardAuthConfig is the seed shape: disabled, empty CIDR
// list, Authelia header names. Boot refuses to start if Enabled flips
// to true while TrustedProxyCIDRs is still empty.
func DefaultForwardAuthConfig() ForwardAuthConfig {
	return ForwardAuthConfig{
		Enabled:           false,
		TrustedProxyCIDRs: []string{},
		Headers: ForwardAuthHeaders{
			User:   "Remote-User",
			Email:  "Remote-Email",
			Name:   "Remote-Name",
			Groups: "Remote-Groups",
		},
	}
}

// ErrForwardAuthInvalidCIDR is returned by SetForwardAuth and
// ValidateForwardAuth when one of the listed CIDRs does not parse.
// Surfaced to the admin UI so they can fix the entry before saving.
var ErrForwardAuthInvalidCIDR = errors.New("forward_auth: invalid CIDR")

// ErrForwardAuthEnabledWithoutCIDR is returned when an admin tries
// to flip Enabled=true while TrustedProxyCIDRs is empty. Same rule
// as boot validation — refusing both keeps the security posture
// consistent regardless of whether config arrived from settings UI
// or a manual DB edit picked up at restart.
var ErrForwardAuthEnabledWithoutCIDR = errors.New("forward_auth: trustedProxyCIDRs must list at least one CIDR when forward-auth is enabled")

// ErrForwardAuthInvalidHeader is returned when a configured header
// name is empty or not a valid HTTP token. The user header is
// required; the others may be empty (we just won't read them).
var ErrForwardAuthInvalidHeader = errors.New("forward_auth: invalid header name")

// ErrForwardAuthInvalidLogoutURL is returned when LogoutURL is set
// but does not parse as an http(s) URL.
var ErrForwardAuthInvalidLogoutURL = errors.New("forward_auth: logoutUrl must be an http(s) URL")

// forwardAuthSetting declares the FORWARD_AUTH row. No Secrets — the
// trust gate is the CIDR allowlist, not a shared secret. Validation is
// the same check boot runs, so admins cannot save a config that would
// refuse the next restart.
var forwardAuthSetting = Setting[ForwardAuthConfig]{
	Key:       SettingForwardAuth,
	Default:   DefaultForwardAuthConfig,
	Normalize: normalizeForwardAuth,
	Validate:  ValidateForwardAuth,
}

// GetForwardAuth loads the FORWARD_AUTH row. A missing row yields
// DefaultForwardAuthConfig() and a nil error so first boot works
// without a seed migration.
func (r *AppSettingsRepo) GetForwardAuth(ctx context.Context) (ForwardAuthConfig, error) {
	return forwardAuthSetting.Get(ctx, r)
}

// SetForwardAuth normalizes, validates, and upserts the FORWARD_AUTH row.
func (r *AppSettingsRepo) SetForwardAuth(ctx context.Context, cfg ForwardAuthConfig) error {
	return forwardAuthSetting.Set(ctx, r, cfg)
}

// SeedForwardAuthIfAbsent writes the default disabled row when none
// exists, so the admin settings UI has a row to render on first boot.
func (r *AppSettingsRepo) SeedForwardAuthIfAbsent(ctx context.Context) error {
	return forwardAuthSetting.SeedIfAbsent(ctx, r)
}

// ValidateForwardAuth runs the same checks SetForwardAuth applies
// internally. Exported so cmd/embookshelf can refuse startup before
// any HTTP traffic is served.
func ValidateForwardAuth(cfg ForwardAuthConfig) error {
	if !cfg.Enabled {
		// Disabled config can hold any garbage — still reject
		// obviously-broken CIDRs so flipping Enabled later doesn't
		// surprise the operator.
		for _, c := range cfg.TrustedProxyCIDRs {
			if c == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(c); err != nil {
				return fmt.Errorf("%w: %q", ErrForwardAuthInvalidCIDR, c)
			}
		}
		if cfg.LogoutURL != "" {
			if err := checkLogoutURL(cfg.LogoutURL); err != nil {
				return err
			}
		}
		return nil
	}
	if len(cfg.TrustedProxyCIDRs) == 0 {
		return ErrForwardAuthEnabledWithoutCIDR
	}
	for _, c := range cfg.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return fmt.Errorf("%w: %q", ErrForwardAuthInvalidCIDR, c)
		}
	}
	if !validHeaderName(cfg.Headers.User) {
		return fmt.Errorf("%w: user header %q", ErrForwardAuthInvalidHeader, cfg.Headers.User)
	}
	for label, h := range map[string]string{
		"email":  cfg.Headers.Email,
		"name":   cfg.Headers.Name,
		"groups": cfg.Headers.Groups,
	} {
		if h == "" {
			continue
		}
		if !validHeaderName(h) {
			return fmt.Errorf("%w: %s header %q", ErrForwardAuthInvalidHeader, label, h)
		}
	}
	if cfg.LogoutURL != "" {
		if err := checkLogoutURL(cfg.LogoutURL); err != nil {
			return err
		}
	}
	return nil
}

func normalizeForwardAuth(cfg ForwardAuthConfig) ForwardAuthConfig {
	out := cfg
	out.LogoutURL = strings.TrimRight(strings.TrimSpace(cfg.LogoutURL), "/")
	out.Headers.User = strings.TrimSpace(cfg.Headers.User)
	out.Headers.Email = strings.TrimSpace(cfg.Headers.Email)
	out.Headers.Name = strings.TrimSpace(cfg.Headers.Name)
	out.Headers.Groups = strings.TrimSpace(cfg.Headers.Groups)
	cleaned := make([]string, 0, len(cfg.TrustedProxyCIDRs))
	for _, c := range cfg.TrustedProxyCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		cleaned = append(cleaned, c)
	}
	out.TrustedProxyCIDRs = cleaned
	if out.Enabled && out.Headers.User == "" {
		out.Headers.User = DefaultForwardAuthConfig().Headers.User
	}
	return out
}

func checkLogoutURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrForwardAuthInvalidLogoutURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrForwardAuthInvalidLogoutURL
	}
	if u.Host == "" {
		return ErrForwardAuthInvalidLogoutURL
	}
	return nil
}

// validHeaderName accepts an RFC 7230 token. We don't allow ":",
// whitespace, or non-ASCII — the chars browsers/proxies guarantee
// for header names.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}
