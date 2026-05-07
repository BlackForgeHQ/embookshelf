package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// SettingsUsersList returns every account. Admin-only (mounted under the
// admin group in the router).
func (h *Handler) SettingsUsersList(c *gin.Context) {
	users, err := h.auth.ListUsers(c.Request.Context())
	if err != nil {
		writeServerError(c, "settings users list", err)
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toUserDTO(u))
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

type createUserReq struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (h *Handler) SettingsUsersCreate(c *gin.Context) {
	var body createUserReq
	if !bindJSON(c, &body) {
		return
	}
	role := model.Role(body.Role)
	if role == "" {
		role = model.RoleUser
	}
	u, err := h.auth.CreateUser(c.Request.Context(), body.Email, body.Name, body.Password, role)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailTaken):
			writeError(c, http.StatusConflict, service.ErrEmailTaken.Error())
		default:
			// Weak password + invalid-role errors surface the raw message —
			// they're user-fixable and don't leak internals.
			writeError(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": toUserDTO(u)})
}

type updateUserRoleReq struct {
	Role string `json:"role"`
}

// SettingsUsersUpdateRole promotes/demotes a user. Refuses to demote the
// last remaining admin (service-layer guard). Demoting an admin
// cascades into un-publishing any shelves they had shared — only
// admins can keep shelves shared (ADR-0017).
func (h *Handler) SettingsUsersUpdateRole(c *gin.Context) {
	var body updateUserRoleReq
	if !bindJSON(c, &body) {
		return
	}
	targetID := c.Param("id")
	role := model.Role(body.Role)
	err := h.auth.SetUserRole(c.Request.Context(), targetID, role)
	switch {
	case err == nil:
		if role == model.RoleUser {
			// Best-effort: a failed cascade shouldn't poison the role
			// change. Public shelves owned by the demoted user remain
			// flagged is_public=true until the next admin un-flips
			// them manually — recoverable, no data lost.
			_ = h.shelf.UnpublishAllForOwner(c.Request.Context(), targetID)
		}
		c.Status(http.StatusNoContent)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "user not found")
	case errors.Is(err, service.ErrLastAdmin):
		writeError(c, http.StatusConflict, service.ErrLastAdmin.Error())
	default:
		writeError(c, http.StatusBadRequest, err.Error())
	}
}

// SettingsUsersDelete removes an account. The service refuses to delete
// the last remaining admin.
func (h *Handler) SettingsUsersDelete(c *gin.Context) {
	err := h.auth.DeleteUser(c.Request.Context(), c.Param("id"))
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "user not found")
	case errors.Is(err, service.ErrLastAdmin):
		writeError(c, http.StatusConflict, service.ErrLastAdmin.Error())
	default:
		writeServerError(c, "settings users delete", err)
	}
}

// callerID returns the authenticated user's ID, or "" when no session is
// attached. The admin guard upstream guarantees a session exists, but
// returning "" instead of panicking keeps this defensive.
func callerID(c *gin.Context) string {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		return ""
	}
	return u.ID
}

// SettingsUsersApprove flips a pending or denied user to active.
func (h *Handler) SettingsUsersApprove(c *gin.Context) {
	u, err := h.auth.ApproveUser(c.Request.Context(), callerID(c), c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"user": toUserDTO(u)})
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "user not found")
	default:
		writeServerError(c, "settings users approve", err)
	}
}

// SettingsUsersDeny flips a pending user to denied. Refuses to deny the
// caller themselves or the last remaining admin.
func (h *Handler) SettingsUsersDeny(c *gin.Context) {
	u, err := h.auth.DenyUser(c.Request.Context(), callerID(c), c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"user": toUserDTO(u)})
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "user not found")
	case errors.Is(err, service.ErrCannotTargetSelf):
		writeError(c, http.StatusBadRequest, service.ErrCannotTargetSelf.Error())
	case errors.Is(err, service.ErrLastAdmin):
		writeError(c, http.StatusConflict, service.ErrLastAdmin.Error())
	default:
		writeServerError(c, "settings users deny", err)
	}
}
