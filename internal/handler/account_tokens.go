// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// apiTokenManager is the handler tier's view of service.APITokenService,
// declared so the token endpoints can be driven against a fake.
type apiTokenManager interface {
	Create(ctx context.Context, userID, name string, ttl time.Duration) (string, model.APIToken, error)
	List(ctx context.Context, userID string) ([]model.APIToken, error)
	ListAll(ctx context.Context) ([]repo.APITokenWithOwner, error)
	Revoke(ctx context.Context, userID, id string) error
	RevokeAny(ctx context.Context, id string) error
	// UserByToken is the bearer middleware's resolve on /mcp.
	UserByToken(ctx context.Context, raw string) (model.User, error)
}

var _ apiTokenManager = (*service.APITokenService)(nil)

// maxAPITokenDays bounds the expiry a client may ask for. Ten years is
// "never" in all but name; anything past it is a typo.
const maxAPITokenDays = 3650

type apiTokenDTO struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"createdAt"`
	LastUsedAt *string `json:"lastUsedAt"`
	ExpiresAt  *string `json:"expiresAt"`
}

type apiTokenOwnerDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type adminAPITokenDTO struct {
	apiTokenDTO
	Owner apiTokenOwnerDTO `json:"owner"`
}

type createAPITokenReq struct {
	Name string `json:"name"`
	// ExpiresInDays 0 or absent means the token never expires.
	ExpiresInDays int `json:"expiresInDays"`
}

func toAPITokenDTO(t model.APIToken) apiTokenDTO {
	return apiTokenDTO{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     service.APITokenPrefix + t.Prefix,
		CreatedAt:  t.CreatedAt.UTC().Format(time.RFC3339),
		LastUsedAt: rfc3339OrNil(t.LastUsedAt),
		ExpiresAt:  rfc3339OrNil(t.ExpiresAt),
	}
}

func rfc3339OrNil(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func writeMCPDisabled(c *gin.Context) {
	writeErrorCode(c, http.StatusServiceUnavailable, CodeMCPDisabled,
		"MCP is not enabled on this instance")
}

// AccountTokensList returns the caller's own tokens. Answers while MCP is
// off too: the tokens still exist, and a user may want to revoke one.
func (h *Handler) AccountTokensList(c *gin.Context, userID string) {
	tokens, err := h.apiTokens.List(c.Request.Context(), userID)
	if err != nil {
		writeServerError(c, "account tokens list", err)
		return
	}
	out := make([]apiTokenDTO, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toAPITokenDTO(t))
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

// AccountTokenCreate mints a token for the caller. The plaintext is in
// this response and nowhere else, ever.
func (h *Handler) AccountTokenCreate(c *gin.Context, userID string) {
	var body createAPITokenReq
	if !bindJSON(c, &body) {
		return
	}
	if body.ExpiresInDays < 0 || body.ExpiresInDays > maxAPITokenDays {
		writeError(c, http.StatusBadRequest, "expiresInDays must be between 0 (never) and 3650")
		return
	}
	ttl := time.Duration(body.ExpiresInDays) * 24 * time.Hour

	raw, tok, err := h.apiTokens.Create(c.Request.Context(), userID, body.Name, ttl)
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, gin.H{"token": raw, "meta": toAPITokenDTO(tok)})
	case errors.Is(err, service.ErrMCPDisabled):
		writeMCPDisabled(c)
	case errors.Is(err, service.ErrAPITokenName):
		writeError(c, http.StatusBadRequest, err.Error())
	default:
		writeServerError(c, "account token create", err)
	}
}

// AccountTokenRevoke deletes one of the caller's tokens. Another user's
// token id is a 404, indistinguishable from an unknown one.
func (h *Handler) AccountTokenRevoke(c *gin.Context, userID string) {
	err := h.apiTokens.Revoke(c.Request.Context(), userID, c.Param("id"))
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "token not found")
	default:
		writeServerError(c, "account token revoke", err)
	}
}
