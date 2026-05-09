// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"time"

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

// ForwardAuthService is the resolver behind the forward-auth
// middleware. It mirrors OIDCService.findOrProvisionUser logic but
// runs without an OIDC token exchange — the proxy is the
// authentication. ADR-0022.
type ForwardAuthService struct {
	settings   *repo.AppSettingsRepo
	users      *repo.UserRepo
	identities *repo.IdentityRepo
}

func NewForwardAuthService(settings *repo.AppSettingsRepo, users *repo.UserRepo, identities *repo.IdentityRepo) *ForwardAuthService {
	return &ForwardAuthService{settings: settings, users: users, identities: identities}
}

// ResolveProxyIdentity is called for every trusted forward-auth hit.
// Returns the resolved user. The contract:
//
//  1. (provider='proxy', subject) hit → return the linked user.
//  2. Email match against an existing user → relink under the
//     'proxy' slot (gated by AllowLocalAccountLinking when the user
//     has no identities yet, mirroring OIDC auto-link).
//  3. Auto-provision when EnableAutoProvisioning OR users table is
//     empty (first-user-becomes-admin carve-out).
//  4. Otherwise return ErrForwardAuthRejected so the middleware
//     emits 401 — admin-pending and admin-denied users also return
//     this rather than ErrForwardAuthPending; forward-auth has no
//     "approval landing page" to redirect to. Mirrors the
//     deny-by-default policy from CONTEXT.md → "Provisioning".
func (s *ForwardAuthService) ResolveProxyIdentity(ctx context.Context, subject, email, name string) (model.User, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return model.User{}, auth.ErrForwardAuthRejected
	}
	email = strings.ToLower(strings.TrimSpace(email))

	// 1) Direct hit.
	if ident, err := s.identities.GetByIssuerSubject(ctx, ProxyIssuer, subject); err == nil {
		u, uerr := s.users.GetByID(ctx, ident.UserID)
		if uerr != nil {
			return model.User{}, uerr
		}
		_ = s.identities.TouchLastLogin(ctx, ident.ID)
		_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())
		switch u.Status {
		case model.UserStatusActive:
			return u, nil
		case model.UserStatusPending, model.UserStatusDenied:
			return model.User{}, auth.ErrForwardAuthRejected
		default:
			return u, nil
		}
	} else if !errors.Is(err, repo.ErrNotFound) {
		return model.User{}, err
	}

	provision, err := s.settings.GetOIDCAutoProvision(ctx)
	if err != nil {
		return model.User{}, err
	}

	// 2) Email auto-link.
	if email != "" {
		existing, err := s.users.GetByEmail(ctx, email)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return model.User{}, err
		}
		if err == nil {
			count, cerr := s.identities.CountByUser(ctx, existing.ID)
			if cerr != nil {
				return model.User{}, cerr
			}
			if !provision.AllowLocalAccountLinking && count == 0 {
				return model.User{}, auth.ErrForwardAuthRejected
			}
			if existing.Status != model.UserStatusActive {
				return model.User{}, auth.ErrForwardAuthRejected
			}
			if _, err := s.identities.RelinkProvider(ctx, existing.ID, repo.ProviderSlugProxy, ProxyIssuer, subject, email); err != nil {
				return model.User{}, err
			}
			_ = s.users.TouchLastSeen(ctx, existing.ID, time.Now())
			return existing, nil
		}
	}

	// 3) Auto-provision. Empty users table = bootstrap admin.
	n, err := s.users.Count(ctx)
	if err != nil {
		return model.User{}, err
	}
	firstUser := n == 0
	if !provision.EnableAutoProvisioning && !firstUser {
		return model.User{}, auth.ErrForwardAuthRejected
	}
	if email == "" {
		// We need an email for the users row (NOT NULL); if the
		// proxy didn't include it, refuse. Same constraint OIDC
		// holds itself to.
		return model.User{}, auth.ErrForwardAuthRejected
	}

	role := model.RoleUser
	if provision.DefaultRole == "admin" {
		role = model.RoleAdmin
	}
	if firstUser {
		role = model.RoleAdmin
	}
	pending := provision.RequireAdminApproval && !firstUser

	var created model.User
	if pending {
		created, err = s.users.CreateOIDCPending(ctx, email, name, role)
	} else {
		created, err = s.users.CreateOIDC(ctx, email, name, role)
	}
	if err != nil {
		return model.User{}, err
	}
	if _, err := s.identities.Insert(ctx, created.ID, repo.ProviderSlugProxy, ProxyIssuer, subject, email); err != nil {
		_ = s.users.Delete(ctx, created.ID)
		return model.User{}, err
	}
	if pending {
		// Auto-provisioned but pending approval — middleware emits
		// 401, exactly as for an unknown identity.
		return model.User{}, auth.ErrForwardAuthRejected
	}
	_ = s.users.TouchLastSeen(ctx, created.ID, time.Now())
	return created, nil
}
