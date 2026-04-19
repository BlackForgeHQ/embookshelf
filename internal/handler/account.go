package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
)

type changePasswordReq struct {
	Current string `json:"current"`
	Next    string `json:"next"`
}

// AccountChangePassword replaces the signed-in user's password after
// verifying the current one. Returns 204 on success; 401 for a bad current
// password, 400 for policy violations (short password, empty next).
func (h *Handler) AccountChangePassword(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var body changePasswordReq
	if !bindJSON(c, &body) {
		return
	}
	if strings.TrimSpace(body.Current) == "" || body.Next == "" {
		writeError(c, http.StatusBadRequest, "current and next passwords are required")
		return
	}
	err := h.auth.ChangePassword(c.Request.Context(), u.ID, body.Current, body.Next)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, service.ErrInvalidCredentials):
		writeError(c, http.StatusUnauthorized, "current password is incorrect")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
	default:
		writeServerError(c, "account change password", err)
	}
}

type updateNameReq struct {
	Name string `json:"name"`
}

// AccountUpdateName updates the signed-in user's display name. Empty string
// clears it (Display() falls back to email).
func (h *Handler) AccountUpdateName(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var body updateNameReq
	if !bindJSON(c, &body) {
		return
	}
	if err := h.auth.UpdateDisplayName(c.Request.Context(), u.ID, body.Name); err != nil {
		writeServerError(c, "account update name", err)
		return
	}
	c.Status(http.StatusNoContent)
}
