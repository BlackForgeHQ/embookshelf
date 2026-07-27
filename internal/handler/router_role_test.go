// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/model"
)

// roleRouterRequest drives a request through the real engine — every
// middleware in the chain, in the order Engine() wires them — with a
// user of the given role already pinned to the context. That is the
// forward-auth entry point (ADR-0022), and RequireAuth short-circuits on
// it, so nothing here touches the database or a session store.
//
// A role-gated route aborts inside RequireRole, before the handler ever
// runs, which is why a zero-value Handler is enough.
func roleRouterRequest(t *testing.T, role model.Role, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// AllowedOrigins has to be non-empty or the CORS middleware panics
	// on "all origins disabled".
	h := &Handler{cfg: config.Config{AllowedOrigins: []string{"http://localhost:5173"}}}
	engine := h.Engine()

	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	// Same-origin, so CSRFGuard passes and a 403 can only come from the
	// role gate under test.
	r.Header.Set("Origin", "http://"+r.Host)
	u := model.User{ID: "u1", Role: role}
	r = r.WithContext(auth.WithUser(r.Context(), &u))

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, r)
	return rec
}

// Writing a reading guide is admin-only. Generating spends the
// instance's LLM key, and a guide is per-book rather than per-user, so a
// non-admin regenerate or hand-edit would overwrite what every other
// reader sees. Reading one stays open, the same split the audiobook
// routes use (ADR-0028 §1).
func TestBookGuideWritesAreAdminOnly(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
	}{
		{"generate", http.MethodPost, ""},
		{"edit", http.MethodPut, `{"about":"mine now"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := roleRouterRequest(t, model.RoleUser, tc.method,
				"/api/v1/books/b1/guide", tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s /books/b1/guide as a non-admin = %d, want 403",
					tc.method, rec.Code)
			}
		})
	}
}

// Control: the harness authenticates a non-admin fine, so the 403s above
// are the role gate and not a broken request.
func TestNonAdminReachesAnUngatedRoute(t *testing.T) {
	rec := roleRouterRequest(t, model.RoleUser, http.MethodGet, "/api/v1/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me as a non-admin = %d, want 200", rec.Code)
	}
}
