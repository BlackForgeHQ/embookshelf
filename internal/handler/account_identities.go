package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// accountIdentitiesDTO is the composite response shape that powers
// the account-panel auth section. See ADR-0001 + CONTEXT.md.
type accountIdentitiesDTO struct {
	HasPassword bool                         `json:"hasPassword"`
	Providers   []accountIdentityProviderDTO `json:"providers"`
}

type accountIdentityProviderDTO struct {
	Provider    string  `json:"provider"`
	DisplayName string  `json:"displayName"`
	Linked      bool    `json:"linked"`
	Email       *string `json:"email,omitempty"`
	LinkedAt    *string `json:"linkedAt,omitempty"`
	LastLoginAt *string `json:"lastLoginAt,omitempty"`
}

// AccountIdentities returns the user's per-provider link state plus
// hasPassword in a single round-trip. Disabled providers are omitted
// so the UI doesn't render dead options.
func (h *Handler) AccountIdentities(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.oidc == nil || h.identities == nil {
		c.JSON(http.StatusOK, accountIdentitiesDTO{
			HasPassword: u.PasswordHash != "",
			Providers:   []accountIdentityProviderDTO{},
		})
		return
	}
	ctx := c.Request.Context()
	pub, err := h.oidc.PublicConfig(ctx)
	if err != nil {
		writeServerError(c, "account identities: oidc public config", err)
		return
	}
	links, err := h.identities.ListByUser(ctx, u.ID)
	if err != nil {
		writeServerError(c, "account identities: list", err)
		return
	}
	bySlug := make(map[string]int, len(links))
	for i := range links {
		bySlug[links[i].Provider] = i
	}
	out := accountIdentitiesDTO{
		HasPassword: u.PasswordHash != "",
		Providers:   make([]accountIdentityProviderDTO, 0, len(pub.Providers)),
	}
	for _, p := range pub.Providers {
		row := accountIdentityProviderDTO{
			Provider:    p.Slug,
			DisplayName: p.Name,
		}
		if i, ok := bySlug[p.Slug]; ok {
			row.Linked = true
			ident := links[i]
			row.Email = ident.Email
			la := ident.LinkedAt.UTC().Format("2006-01-02T15:04:05Z")
			row.LinkedAt = &la
			if ident.LastLoginAt != nil {
				ll := ident.LastLoginAt.UTC().Format("2006-01-02T15:04:05Z")
				row.LastLoginAt = &ll
			}
		}
		out.Providers = append(out.Providers, row)
	}
	c.JSON(http.StatusOK, out)
}

// AccountOIDCLink starts the OAuth flow for binding an identity to
// the signed-in user. The redirect target lives at the existing
// /api/v1/auth/oidc/callback — the state token discriminates intent.
func (h *Handler) AccountOIDCLink(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.oidc == nil {
		writeError(c, http.StatusNotFound, "OIDC is not configured")
		return
	}
	slug := c.Param("slug")
	authURL, err := h.oidc.AuthURLForLink(c.Request.Context(), slug, requestOrigin(c), u.ID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOIDCUnknownProvider):
			writeError(c, http.StatusNotFound, "unknown provider")
		case errors.Is(err, service.ErrOIDCDisabled), errors.Is(err, service.ErrOIDCNotConfigured):
			writeError(c, http.StatusNotFound, "provider is not enabled")
		default:
			writeServerError(c, "account oidc link", err)
		}
		return
	}
	c.Redirect(http.StatusFound, authURL)
}

// AccountOIDCUnlink removes the identity row for the given provider.
// Refuses with 409 when the lockout guard would fire (no other
// credential remains).
func (h *Handler) AccountOIDCUnlink(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.identities == nil {
		writeError(c, http.StatusNotFound, "identities not available")
		return
	}
	provider := strings.TrimSpace(c.Param("provider"))
	if provider == "" {
		writeError(c, http.StatusBadRequest, "provider is required")
		return
	}
	deleted, err := h.identities.DeleteWithGuard(c.Request.Context(), u.ID, provider)
	switch {
	case deleted:
		c.Status(http.StatusNoContent)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "identity not linked")
	case errors.Is(err, repo.ErrIdentityLockout):
		writeError(c, http.StatusConflict, "set a password before unlinking the last sign-in method")
	default:
		writeServerError(c, "account oidc unlink", err)
	}
}

type setInitialPasswordReq struct {
	Next string `json:"next"`
}

// AccountSetInitialPassword is the OIDC-only-user "I want a password
// too" flow. Refuses with 409 if a password is already on record so
// callers can't accidentally take over the account this way.
func (h *Handler) AccountSetInitialPassword(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var body setInitialPasswordReq
	if !bindJSON(c, &body) {
		return
	}
	if body.Next == "" {
		writeError(c, http.StatusBadRequest, "next password is required")
		return
	}
	err := h.auth.SetInitialPassword(c.Request.Context(), u.ID, body.Next)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, service.ErrPasswordAlreadySet):
		writeError(c, http.StatusConflict, "password already set; use change-password")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
	default:
		writeServerError(c, "account set initial password", err)
	}
}
