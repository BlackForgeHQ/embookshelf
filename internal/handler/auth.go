package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/view/page"
)

// LoginPage renders the login form. If already authenticated, redirect to the
// original `next` target or the dashboard.
func (h *Handler) LoginPage(c *gin.Context) {
	if auth.UserFromContext(c.Request.Context()) != nil {
		c.Redirect(http.StatusFound, "/app")
		return
	}
	next := sanitizeNext(c.Query("next"))
	render(c, page.Login(next, "", ""))
}

// Login handles the POST from the login form.
func (h *Handler) Login(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	password := c.PostForm("password")
	next := sanitizeNext(c.PostForm("next"))

	sess, _, err := h.auth.Login(c.Request.Context(), email, password, c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			c.Writer.WriteHeader(http.StatusUnauthorized)
			render(c, page.Login(next, email, "Invalid email or password."))
			return
		}
		slog.Error("login", "err", err)
		c.String(http.StatusInternalServerError, "login failed")
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.Redirect(http.StatusSeeOther, next)
}

// Logout destroys the current session.
func (h *Handler) Logout(c *gin.Context) {
	if cookie, err := c.Cookie(auth.SessionCookieName); err == nil && cookie != "" {
		_ = h.auth.Logout(c.Request.Context(), cookie)
	}
	auth.ClearSessionCookie(c)
	c.Redirect(http.StatusSeeOther, "/login")
}

// SignupPage renders the first-run bootstrap signup page when no users exist.
func (h *Handler) SignupPage(c *gin.Context) {
	open, err := h.auth.SignupEnabled(c.Request.Context())
	if err != nil {
		slog.Error("signup enabled check", "err", err)
		c.String(http.StatusInternalServerError, "signup unavailable")
		return
	}
	if !open {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	render(c, page.Signup("", "", ""))
}

func (h *Handler) Signup(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	name := strings.TrimSpace(c.PostForm("name"))
	password := c.PostForm("password")

	sess, _, err := h.auth.Signup(c.Request.Context(), email, name, password, c.Request.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrSignupClosed):
			c.Redirect(http.StatusFound, "/login")
			return
		case errors.Is(err, service.ErrEmailTaken):
			c.Writer.WriteHeader(http.StatusConflict)
			render(c, page.Signup(email, name, "That email is already registered."))
			return
		case errors.Is(err, auth.ErrWeakPassword):
			c.Writer.WriteHeader(http.StatusBadRequest)
			render(c, page.Signup(email, name, "Password must be at least 8 characters."))
			return
		default:
			slog.Error("signup", "err", err)
			c.Writer.WriteHeader(http.StatusInternalServerError)
			render(c, page.Signup(email, name, "Could not create account."))
			return
		}
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.Redirect(http.StatusSeeOther, "/app")
}

// sanitizeNext ensures the post-login redirect target is a same-origin,
// absolute-path URL. Anything else falls back to the dashboard.
func sanitizeNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/app"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "/app"
	}
	if u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return "/app"
	}
	if strings.HasPrefix(u.Path, "/login") || strings.HasPrefix(u.Path, "/signup") {
		return "/app"
	}
	return u.RequestURI()
}
