// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ExternalIdentity is the set of facts any External identity provider
// hands the Provisioner: the OIDC callback after token exchange, the
// forward-auth middleware after header trust. CONTEXT.md → "External
// identity provider".
type ExternalIdentity struct {
	Provider string // user_identities.provider slug: google|github|generic|proxy
	Issuer   string
	Subject  string
	Email    string
	Name     string
}

// ProvisionStatus is the semantic outcome of a Provision call. Adapters
// map these to their own error vocabulary (ErrOIDCPendingApproval,
// auth.ErrForwardAuthRejected, ...) — policy here, presentation there.
type ProvisionStatus string

const (
	// ProvisionResolved — active user found, linked, or created; log them in.
	ProvisionResolved ProvisionStatus = "resolved"
	// ProvisionPendingApproval — user exists (or was just created) but
	// awaits admin approval. Result.User is set.
	ProvisionPendingApproval ProvisionStatus = "pending_approval"
	// ProvisionDenied — user was explicitly refused by an admin.
	ProvisionDenied ProvisionStatus = "denied"
	// ProvisionNotAllowed — policy refused: linking gated off, or
	// auto-provisioning disabled for a non-first user.
	ProvisionNotAllowed ProvisionStatus = "not_allowed"
	// ProvisionEmailRequired — provider supplied no email claim and a
	// users row cannot be created without one.
	ProvisionEmailRequired ProvisionStatus = "email_required"
)

// ProvisionResult carries the outcome. User is set for Resolved and
// PendingApproval.
type ProvisionResult struct {
	Status ProvisionStatus
	User   model.User
}

type provisionerSettings interface {
	GetOIDCAutoProvision(ctx context.Context) (repo.OIDCAutoProvisionDetails, error)
}

type provisionerUsers interface {
	GetByID(ctx context.Context, id string) (model.User, error)
	GetByEmail(ctx context.Context, email string) (model.User, error)
	Count(ctx context.Context) (int, error)
	CreateOIDC(ctx context.Context, email, name string, role model.Role) (model.User, error)
	CreateOIDCPending(ctx context.Context, email, name string, role model.Role) (model.User, error)
	Delete(ctx context.Context, id string) error
	TouchLastSeen(ctx context.Context, id string, at time.Time) error
}

type provisionerIdentities interface {
	GetByIssuerSubject(ctx context.Context, issuer, subject string) (model.Identity, error)
	CountByUser(ctx context.Context, userID string) (int, error)
	RelinkProvider(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error)
	Insert(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error)
	TouchLastLogin(ctx context.Context, id string) error
}

// Provisioner is the single implementation of the Provisioning policy
// (CONTEXT.md → "Provisioning", "Auto-link"): identity match → email
// auto-link → auto-provision, with the first-user-becomes-admin
// carve-out and the admin-approval gate. Both External identity
// provider adapters call it; neither owns the policy.
type Provisioner struct {
	settings   provisionerSettings
	users      provisionerUsers
	identities provisionerIdentities
}

func NewProvisioner(settings provisionerSettings, users provisionerUsers, identities provisionerIdentities) *Provisioner {
	return &Provisioner{settings: settings, users: users, identities: identities}
}

// Provision resolves an External identity to a user per the policy.
// Errors are infrastructure failures only; every policy refusal is a
// ProvisionStatus.
func (p *Provisioner) Provision(ctx context.Context, ident ExternalIdentity) (ProvisionResult, error) {
	subject := strings.TrimSpace(ident.Subject)
	if subject == "" {
		return ProvisionResult{Status: ProvisionNotAllowed}, nil
	}
	email := strings.ToLower(strings.TrimSpace(ident.Email))

	// 1) Match by identity. Existing users still clear the status gate —
	//    pending users have not been approved yet, denied users have been
	//    explicitly refused.
	if existing, err := p.identities.GetByIssuerSubject(ctx, ident.Issuer, subject); err == nil {
		u, uerr := p.users.GetByID(ctx, existing.UserID)
		if uerr != nil {
			return ProvisionResult{}, uerr
		}
		_ = p.identities.TouchLastLogin(ctx, existing.ID)
		switch u.Status {
		case model.UserStatusPending:
			return ProvisionResult{Status: ProvisionPendingApproval, User: u}, nil
		case model.UserStatusDenied:
			return ProvisionResult{Status: ProvisionDenied}, nil
		default:
			_ = p.users.TouchLastSeen(ctx, u.ID, time.Now())
			return ProvisionResult{Status: ProvisionResolved, User: u}, nil
		}
	} else if !errors.Is(err, repo.ErrNotFound) {
		return ProvisionResult{}, err
	}

	provision, err := p.settings.GetOIDCAutoProvision(ctx)
	if err != nil {
		return ProvisionResult{}, err
	}

	// 2) Match by email and Auto-link. Status-gated on both auth
	//    surfaces: a pending or denied user never links into a session
	//    via a second provider. AllowLocalAccountLinking gates only the
	//    first identity (a true crossover from local password to SSO);
	//    once a user has one, further providers attach without the flag.
	if email != "" {
		existing, gerr := p.users.GetByEmail(ctx, email)
		if gerr != nil && !errors.Is(gerr, repo.ErrNotFound) {
			return ProvisionResult{}, gerr
		}
		if gerr == nil {
			switch existing.Status {
			case model.UserStatusPending:
				return ProvisionResult{Status: ProvisionPendingApproval, User: existing}, nil
			case model.UserStatusDenied:
				return ProvisionResult{Status: ProvisionDenied}, nil
			}
			count, cerr := p.identities.CountByUser(ctx, existing.ID)
			if cerr != nil {
				return ProvisionResult{}, cerr
			}
			if !provision.AllowLocalAccountLinking && count == 0 {
				return ProvisionResult{Status: ProvisionNotAllowed}, nil
			}
			if _, err := p.identities.RelinkProvider(ctx, existing.ID, ident.Provider, ident.Issuer, subject, email); err != nil {
				return ProvisionResult{}, err
			}
			_ = p.users.TouchLastSeen(ctx, existing.ID, time.Now())
			return ProvisionResult{Status: ProvisionResolved, User: existing}, nil
		}
	}

	// 3) Auto-provision. Empty users table bootstraps an admin even with
	//    provisioning off — otherwise an admin-less instance with
	//    approval-required is unrecoverable.
	n, err := p.users.Count(ctx)
	if err != nil {
		return ProvisionResult{}, err
	}
	firstUser := n == 0
	if !provision.EnableAutoProvisioning && !firstUser {
		return ProvisionResult{Status: ProvisionNotAllowed}, nil
	}
	if email == "" {
		return ProvisionResult{Status: ProvisionEmailRequired}, nil
	}

	role := model.RoleUser
	if provision.DefaultRole == "admin" || firstUser {
		role = model.RoleAdmin
	}
	pending := provision.RequireAdminApproval && !firstUser

	var created model.User
	if pending {
		created, err = p.users.CreateOIDCPending(ctx, email, ident.Name, role)
	} else {
		created, err = p.users.CreateOIDC(ctx, email, ident.Name, role)
	}
	if err != nil {
		return ProvisionResult{}, err
	}
	if _, err := p.identities.Insert(ctx, created.ID, ident.Provider, ident.Issuer, subject, email); err != nil {
		// Identity insert failed after user create; clean up so the
		// orphan row doesn't block a future login by the same email.
		_ = p.users.Delete(ctx, created.ID)
		return ProvisionResult{}, err
	}
	if pending {
		return ProvisionResult{Status: ProvisionPendingApproval, User: created}, nil
	}
	_ = p.users.TouchLastSeen(ctx, created.ID, time.Now())
	return ProvisionResult{Status: ProvisionResolved, User: created}, nil
}
