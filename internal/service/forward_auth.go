// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ProxyIssuer is the synthetic issuer string stamped on every
// `user_identities` row created by the forward-auth path. The OIDC
// callback uses the IdP's discovery URL; here we use a constant
// because there is no such URL — the contract is "trust the proxy."
// Distinct value also makes the rows easy to spot in psql.
const ProxyIssuer = "forward-auth://proxy"

// NewForwardAuthRuntime builds the middleware's runtime config from a
// persisted FORWARD_AUTH row. Boot and the settings handler both go
// through here so a hot-reloaded config is byte-identical to the one a
// restart would produce.
func NewForwardAuthRuntime(row repo.ForwardAuthConfig) (*auth.ForwardAuthConfig, error) {
	return auth.NewForwardAuthConfig(
		row.Enabled,
		row.TrustedProxyCIDRs,
		row.Headers.User,
		row.Headers.Email,
		row.Headers.Name,
		row.Headers.Groups,
		row.LogoutURL,
		row.HideLocalLogin,
	)
}

// ForwardAuthService is the resolver behind the forward-auth
// middleware. It is a thin adapter over the Provisioner — the single
// implementation of the Provisioning policy shared with the OIDC
// callback — run without a token exchange because the proxy is the
// authentication. ADR-0022.
type ForwardAuthService struct {
	prov *Provisioner
}

func NewForwardAuthService(settings *repo.AppSettingsRepo, users *repo.UserRepo, identities *repo.IdentityRepo) *ForwardAuthService {
	return &ForwardAuthService{prov: NewProvisioner(settings, users, identities)}
}

// ResolveProxyIdentity is called for every trusted forward-auth hit.
// Every non-resolved Provisioning outcome — pending, denied, policy
// refusal, missing email — maps to ErrForwardAuthRejected so the
// middleware emits 401: forward-auth has no "approval landing page"
// to redirect to. Mirrors the deny-by-default policy from CONTEXT.md
// → "Provisioning".
func (s *ForwardAuthService) ResolveProxyIdentity(ctx context.Context, subject, email, name string) (model.User, error) {
	res, err := s.prov.Provision(ctx, ExternalIdentity{
		Provider: repo.ProviderSlugProxy,
		Issuer:   ProxyIssuer,
		Subject:  subject,
		Email:    email,
		Name:     name,
	})
	if err != nil {
		return model.User{}, err
	}
	if res.Status != ProvisionResolved {
		return model.User{}, auth.ErrForwardAuthRejected
	}
	return res.User, nil
}
