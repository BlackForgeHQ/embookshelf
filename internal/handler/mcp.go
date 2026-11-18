// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
)

// mcpPath is where the MCP server answers (ADR-0039). Named once because
// the router mounts it and CSRFGuard exempts it, and the two must agree.
const mcpPath = "/mcp"

// requireMCPEnabled refuses every /mcp request while the admin switch is
// off — before the token is even looked up, so a disabled instance never
// reads api_tokens or records a use.
func (h *Handler) requireMCPEnabled(c *gin.Context) {
	if !h.mcpEnabled() {
		writeMCPDisabled(c)
		return
	}
	c.Next()
}

// tokenResolver is what the bearer middleware resolves through. Without a
// token service every token is refused: the route fails closed rather
// than mounting without authentication.
func (h *Handler) tokenResolver() auth.TokenResolver {
	if h.apiTokens == nil {
		return refuseAllTokens{}
	}
	return h.apiTokens
}

type refuseAllTokens struct{}

func (refuseAllTokens) UserByToken(context.Context, string) (model.User, error) {
	return model.User{}, auth.ErrInvalidToken
}

// MCP hands the authenticated request to the MCP server.
func (h *Handler) MCP(c *gin.Context) {
	if h.mcpServer == nil {
		writeError(c, http.StatusServiceUnavailable, "MCP server unavailable")
		return
	}
	h.mcpServer.ServeHTTP(c.Writer, c.Request)
}
