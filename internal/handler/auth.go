package handler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// userDTO is the canonical user shape the SPA consumes. camelCase on the
// wire matches the TS types that frontend/src/data/mock.ts already exports.
type userDTO struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Display    string `json:"display"`
	Initials   string `json:"initials"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt,omitempty"`
}

func toUserDTO(u model.User) userDTO {
	d := userDTO{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		Role:      string(u.Role),
		Display:   u.Display(),
		Initials:  u.Initials(),
		CreatedAt: u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if u.LastSeenAt != nil {
		d.LastSeenAt = u.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return d
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signupReq struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type signupStatus struct {
	Enabled bool `json:"enabled"`
}

// Login authenticates a user, sets the session cookie, and returns the user DTO.
func (h *Handler) Login(c *gin.Context) {
	var body loginReq
	if !bindJSON(c, &body) {
		return
	}
	body.Email = normalizeEmail(body.Email)
	if body.Email == "" || body.Password == "" {
		writeError(c, http.StatusBadRequest, "email and password are required")
		return
	}

	sess, u, err := h.auth.Login(c.Request.Context(), body.Email, body.Password, c.Request.UserAgent())
	if err != nil {
		switch {
		case errIsOneOf(err, service.ErrInvalidCredentials):
			writeError(c, http.StatusUnauthorized, "invalid email or password")
		default:
			writeServerError(c, "login", err)
		}
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.JSON(http.StatusOK, gin.H{"user": toUserDTO(u)})
}

// Logout clears the session cookie and destroys the server-side session.
// Idempotent — returns 204 even if no session was attached.
func (h *Handler) Logout(c *gin.Context) {
	if cookie, err := c.Cookie(auth.SessionCookieName); err == nil && cookie != "" {
		_ = h.auth.Logout(c.Request.Context(), cookie)
	}
	auth.ClearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

// SignupStatus tells the SPA whether the signup form should be shown.
func (h *Handler) SignupStatus(c *gin.Context) {
	open, err := h.auth.SignupEnabled(c.Request.Context())
	if err != nil {
		writeServerError(c, "signup status", err)
		return
	}
	c.JSON(http.StatusOK, signupStatus{Enabled: open})
}

// Signup handles the first-run bootstrap flow — creates the admin user and
// issues a session. Returns 403 if signup has already been closed.
func (h *Handler) Signup(c *gin.Context) {
	var body signupReq
	if !bindJSON(c, &body) {
		return
	}
	body.Email = normalizeEmail(body.Email)
	body.Name = strings.TrimSpace(body.Name)
	if body.Email == "" || body.Password == "" {
		writeError(c, http.StatusBadRequest, "email and password are required")
		return
	}

	sess, u, err := h.auth.Signup(c.Request.Context(), body.Email, body.Name, body.Password, c.Request.UserAgent())
	if err != nil {
		switch {
		case errIsOneOf(err, service.ErrSignupClosed):
			writeError(c, http.StatusForbidden, service.ErrSignupClosed.Error())
		case errIsOneOf(err, service.ErrEmailTaken):
			writeError(c, http.StatusConflict, service.ErrEmailTaken.Error())
		case errIsOneOf(err, auth.ErrWeakPassword):
			writeError(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
		default:
			writeServerError(c, "signup", err)
		}
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.JSON(http.StatusCreated, gin.H{"user": toUserDTO(u)})
}

// Me returns the authenticated user. RequireAuth middleware has already
// attached it to the request context, so 401s never reach this function.
func (h *Handler) Me(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		// Defensive — the router should never route an unauthed request here.
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toUserDTO(*u)})
}

func normalizeEmail(s string) string {
	u, err := url.QueryUnescape(s)
	if err != nil {
		u = s
	}
	return strings.TrimSpace(strings.ToLower(u))
}
