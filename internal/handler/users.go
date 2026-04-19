package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

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
// last remaining admin (service-layer guard).
func (h *Handler) SettingsUsersUpdateRole(c *gin.Context) {
	var body updateUserRoleReq
	if !bindJSON(c, &body) {
		return
	}
	err := h.auth.SetUserRole(c.Request.Context(), c.Param("id"), model.Role(body.Role))
	switch {
	case err == nil:
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
