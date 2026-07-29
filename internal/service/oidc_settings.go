// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"fmt"

	"github.com/blackforge/embookshelf/internal/repo"
)

// ErrOIDCIncomplete is returned when a provider is being enabled without
// the credentials it needs. The message names the provider, because an
// admin looking at three panels has to know which one to fix.
var ErrOIDCIncomplete = fmt.Errorf("oidc provider is missing required settings")

// OIDCSubmission is one settings save: every provider row, the
// auto-provision policy and the force-only flag, as one decision.
//
// Secrets arrive already resolved to plaintext. The keep-versus-clear
// contract stays with the handler tier that owns the wire shape, and
// this module takes what that resolved — the same placement that lets
// AppSettingsRepo hold the Cipher (ADR-0010).
type OIDCSubmission struct {
	Google        repo.OAuthPresetConfig
	GitHub        repo.OAuthPresetConfig
	Generic       repo.GenericOIDCConfig
	AutoProvision repo.OIDCAutoProvisionDetails
	ForceOnly     bool
}

// oidcRowStore prepares and commits the rows a submission becomes.
type oidcRowStore interface {
	PrepareOIDCRows(sub OIDCSubmission) ([]repo.SettingRow, error)
	SetRows(ctx context.Context, rows []repo.SettingRow) error
}

// forceOnlyGuard is the Lockout guard: it refuses a force-only
// transition that would leave nobody able to sign in.
type forceOnlyGuard interface {
	ValidateForceOnlyTransition(ctx context.Context, forceOnly bool) error
}

// OIDCSettingsService applies an OIDC settings submission.
//
// It exists because the handler held the policy: defaults for scopes and
// every claim-mapping field, the per-provider enable rules, and five
// sequential writes with the lockout guard sitting between the fourth
// and the fifth — so a refused force-only left the provider changes
// applied. None of it was testable without an HTTP caller (#195).
type OIDCSettingsService struct {
	store oidcRowStore
	guard forceOnlyGuard
}

func NewOIDCSettingsService(store oidcRowStore, guard forceOnlyGuard) *OIDCSettingsService {
	return &OIDCSettingsService{store: store, guard: guard}
}

// AppSettingsOIDCRows adapts AppSettingsRepo to what this module needs.
//
// A thin adapter rather than a wider interface: the submission type is
// this package's, the row preparation is the repo's, and neither has to
// know the other's shape.
type AppSettingsOIDCRows struct {
	Repo *repo.AppSettingsRepo
}

func (a AppSettingsOIDCRows) PrepareOIDCRows(sub OIDCSubmission) ([]repo.SettingRow, error) {
	return a.Repo.PrepareOIDCSettingRows(repo.OIDCRows{
		Google:        sub.Google,
		GitHub:        sub.GitHub,
		Generic:       sub.Generic,
		AutoProvision: sub.AutoProvision,
		ForceOnly:     sub.ForceOnly,
	})
}

func (a AppSettingsOIDCRows) SetRows(ctx context.Context, rows []repo.SettingRow) error {
	return a.Repo.SetRows(ctx, rows)
}

// Apply validates a submission and commits it, or commits nothing.
//
// Order matters and is the fix: everything that can refuse the
// submission runs before anything is written, and what is written goes
// in one transaction. An instance is configured the way the admin asked
// or the way it already was, never half of each.
func (s *OIDCSettingsService) Apply(ctx context.Context, sub OIDCSubmission) error {
	if err := validateProviders(sub); err != nil {
		return err
	}
	if s.guard != nil {
		if err := s.guard.ValidateForceOnlyTransition(ctx, sub.ForceOnly); err != nil {
			return err
		}
	}
	rows, err := s.store.PrepareOIDCRows(sub)
	if err != nil {
		return err
	}
	return s.store.SetRows(ctx, rows)
}

// validateProviders holds the enable transitions: a provider being
// turned on must carry what it takes to complete a login. A provider
// left off needs nothing, which is how one is turned off.
func validateProviders(sub OIDCSubmission) error {
	if sub.Google.Enabled && (sub.Google.ClientID == "" || sub.Google.ClientSecret == "") {
		return fmt.Errorf("%w: Google needs a clientId and a clientSecret to enable", ErrOIDCIncomplete)
	}
	if sub.GitHub.Enabled && (sub.GitHub.ClientID == "" || sub.GitHub.ClientSecret == "") {
		return fmt.Errorf("%w: GitHub needs a clientId and a clientSecret to enable", ErrOIDCIncomplete)
	}
	// The generic provider needs no secret: a public client doing PKCE
	// against its own issuer is a legitimate configuration.
	if sub.Generic.Enabled && (sub.Generic.ClientID == "" || sub.Generic.IssuerURI == "") {
		return fmt.Errorf("%w: Generic OIDC needs a clientId and an issuerUri to enable", ErrOIDCIncomplete)
	}
	return nil
}
