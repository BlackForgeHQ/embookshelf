// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type fakeResolver struct {
	user  model.User
	err   error
	gotID string
}

func (f *fakeResolver) UserBySession(_ context.Context, sessionID string) (model.User, error) {
	f.gotID = sessionID
	return f.user, f.err
}

func newRouter(handlers ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	for _, h := range handlers {
		r.Use(h)
	}
	return r
}

func TestRequireAuthMissingCookie(t *testing.T) {
	r := newRouter(RequireAuth(&fakeResolver{}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "authentication required") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestRequireAuthInvalidSessionClearsCookie(t *testing.T) {
	resolver := &fakeResolver{err: repo.ErrNotFound}
	r := newRouter(RequireAuth(resolver))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "stale"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if resolver.gotID != "stale" {
		t.Errorf("resolver got %q, want %q", resolver.gotID, "stale")
	}
	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, SessionCookieName+"=") || !strings.Contains(setCookie, "Max-Age=0") {
		t.Errorf("expected cookie cleared, got %q", setCookie)
	}
}

func TestRequireAuthGenericErrorDoesNotClear(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("boom")}
	r := newRouter(RequireAuth(resolver))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "x"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Errorf("expected no Set-Cookie on generic error, got %q", w.Header().Get("Set-Cookie"))
	}
}

func TestRequireAuthAttachesUser(t *testing.T) {
	resolver := &fakeResolver{user: model.User{ID: "u1", Email: "a@b.c", Role: model.RoleUser}}
	r := newRouter(RequireAuth(resolver))

	var seen *model.User
	r.GET("/", func(c *gin.Context) {
		seen = UserFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "good"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if seen == nil || seen.ID != "u1" {
		t.Errorf("user in context = %+v, want id=u1", seen)
	}
}

func TestRequireRoleNoUser(t *testing.T) {
	r := newRouter(RequireRole(model.RoleAdmin))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRequireRoleMatchAndMismatch(t *testing.T) {
	cases := []struct {
		name     string
		userRole model.Role
		allow    []model.Role
		want     int
	}{
		{"admin allowed", model.RoleAdmin, []model.Role{model.RoleAdmin}, http.StatusOK},
		{"user denied", model.RoleUser, []model.Role{model.RoleAdmin}, http.StatusForbidden},
		{"multi role match", model.RoleUser, []model.Role{model.RoleAdmin, model.RoleUser}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				ctx := WithUser(c.Request.Context(), &model.User{ID: "u", Role: tc.userRole})
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			}, RequireRole(tc.allow...))
			r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

			if w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestCSRFGuardSafeMethodsPass(t *testing.T) {
	r := newRouter(CSRFGuard(nil))
	r.Any("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(m, "/", nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", m, w.Code)
		}
	}
}

func TestCSRFGuardMissingOrigin(t *testing.T) {
	r := newRouter(CSRFGuard(nil))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "missing origin") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestCSRFGuardBadOrigin(t *testing.T) {
	r := newRouter(CSRFGuard(nil))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Origin", "::not-a-url")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad origin") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestCSRFGuardOriginMatchesHost(t *testing.T) {
	r := newRouter(CSRFGuard(nil))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "app.example.com"
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestCSRFGuardRefererFallback(t *testing.T) {
	r := newRouter(CSRFGuard(nil))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "app.example.com"
	req.Header.Set("Referer", "https://app.example.com/page")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestCSRFGuardTrustedOrigin(t *testing.T) {
	r := newRouter(CSRFGuard([]string{"  ", "", "http://localhost:5173"}))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "localhost:6060"
	req.Header.Set("Origin", "http://LOCALHOST:5173")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

func TestCSRFGuardOriginMismatch(t *testing.T) {
	r := newRouter(CSRFGuard([]string{"http://localhost:5173"}))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "app.example.com"
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "origin mismatch") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestCSRFGuardWildcardAllowsAny(t *testing.T) {
	r := newRouter(CSRFGuard([]string{"*"}))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "app.example.com"
	req.Header.Set("Origin", "https://anywhere.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestSetSessionCookie(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		SetSessionCookie(c, "sess-123", time.Hour, true)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	got := w.Header().Get("Set-Cookie")
	for _, want := range []string{
		SessionCookieName + "=sess-123",
		"Path=/",
		"Max-Age=3600",
		"HttpOnly",
		"Secure",
		"SameSite=Lax",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cookie %q missing %q", got, want)
		}
	}
}

func TestClearSessionCookie(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		ClearSessionCookie(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	got := w.Header().Get("Set-Cookie")
	if !strings.Contains(got, SessionCookieName+"=") {
		t.Errorf("missing cookie name in %q", got)
	}
	if !strings.Contains(got, "Max-Age=0") {
		t.Errorf("missing Max-Age=0 in %q", got)
	}
}
