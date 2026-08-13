// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
)

// The seam is tested once, here, mirroring bookscope_test.go: this file is
// where the preamble's behaviour — resolve the session user, fail closed
// when it's absent — is pinned for every user-scoped route.

const userScopeTestUserID = "c1c1c1c1-0002-4002-8002-000000000001"

// userScopeRequest drives one wrapped handler. withUser mirrors what
// auth.RequireAuth would have attached; omitting it is the unauthenticated
// case.
func userScopeRequest(t *testing.T, fn gin.HandlerFunc, withUser bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	if withUser {
		c.Request = c.Request.WithContext(
			auth.WithUser(c.Request.Context(), &model.User{ID: userScopeTestUserID}))
	}
	fn(c)
	return rec
}

func TestUserScopedPassesTheSessionUserToTheBody(t *testing.T) {
	h := &Handler{}

	var got string
	ran := false
	rec := userScopeRequest(t, h.userScoped(func(c *gin.Context, userID string) {
		ran, got = true, userID
		c.Status(http.StatusOK)
	}), true)

	if !ran {
		t.Fatal("handler body never ran for an authenticated request")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != userScopeTestUserID {
		t.Errorf("body got user %q, want the session user", got)
	}
}

// The fail-closed claim. An unauthenticated request must not reach a
// handler body — same status and message requireUserID has always written.
func TestUserScopedUnauthenticatedNeverReachesTheBody(t *testing.T) {
	h := &Handler{}

	ran := false
	rec := userScopeRequest(t, h.userScoped(func(*gin.Context, string) {
		ran = true
	}), false)

	if ran {
		t.Fatal("handler body ran for an unauthenticated request")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authentication required") {
		t.Errorf("body = %s, want the auth-required message", rec.Body.String())
	}
}
