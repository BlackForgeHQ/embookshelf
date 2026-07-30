// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/service"
)

// TestRedirectURIMatchesLoginFlow pins the invariant a forwarded host
// used to break: the redirect URI the admin panel tells the operator to
// register at the IdP must be byte-identical to the one the login flow
// actually sends. When they diverged the IdP answered
// redirect_uri_mismatch and nothing in embookshelf explained why.
func TestRedirectURIMatchesLoginFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/oidc", nil)
	c.Request.Host = "embookshelf:6060"
	c.Request.Header.Set("X-Forwarded-Proto", "https")
	c.Request.Header.Set("X-Forwarded-Host", "books.example.com")

	// What OIDCLogin hands the service, which appends the callback path
	// registered in router.go.
	sent := requestOrigin(c) + "/api/v1/auth/oidc/callback"

	if got := (&Handler{}).buildRedirectURI(c); got != sent {
		t.Fatalf("panel displays %q, login flow sends %q", got, sent)
	}
}

// TestRedirectURIPrefersAppURL covers the other half of the same
// divergence: when APP_URL is configured the login flow sends it and
// ignores the request entirely (service.resolveRedirectURL), so the
// panel must display that and not a header-derived guess.
func TestRedirectURIPrefersAppURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &Handler{cfg: config.Config{AppURL: "https://books.example.com"}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/oidc", nil)
	c.Request.Host = "embookshelf:6060"
	c.Request.Header.Set("X-Forwarded-Host", "stale.example.com")

	want := "https://books.example.com/api/v1/auth/oidc/callback"
	if got := h.buildRedirectURI(c); got != want {
		t.Fatalf("panel displays %q, login flow sends %q", got, want)
	}
}

// TestRequestOriginProxyHeaders covers the proxy shapes the three
// origin surfaces used to disagree about.
func TestRequestOriginProxyHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name  string
		host  string
		proto string
		fwdTo string
		want  string
	}{
		{
			name: "no proxy headers",
			host: "localhost:6060",
			want: "http://localhost:6060",
		},
		{
			name:  "forwarded proto only",
			host:  "localhost:6060",
			proto: "https",
			want:  "https://localhost:6060",
		},
		{
			name:  "forwarded host only",
			host:  "embookshelf:6060",
			fwdTo: "books.example.com",
			want:  "http://books.example.com",
		},
		{
			name:  "both",
			host:  "embookshelf:6060",
			proto: "https",
			fwdTo: "books.example.com",
			want:  "https://books.example.com",
		},
		{
			// Two proxies in the chain each append their hop; the
			// client-facing one is first and is the one that matters.
			name:  "comma-separated proto",
			host:  "embookshelf:6060",
			proto: "https, http",
			fwdTo: "books.example.com, embookshelf:6060",
			want:  "https://books.example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			c.Request.Host = tc.host
			if tc.proto != "" {
				c.Request.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.fwdTo != "" {
				c.Request.Header.Set("X-Forwarded-Host", tc.fwdTo)
			}

			if got := requestOrigin(c); got != tc.want {
				t.Errorf("requestOrigin = %q, want %q", got, tc.want)
			}
			// The panel's display and the OPDS feed base are the same
			// answer, not re-derivations of it.
			if got, want := (&Handler{}).buildRedirectURI(c), tc.want+"/api/v1/auth/oidc/callback"; got != want {
				t.Errorf("buildRedirectURI = %q, want %q", got, want)
			}
			if got := opdsBase(c); got != tc.want {
				t.Errorf("opdsBase = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOIDCErrorCodeMaps(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{service.ErrOIDCStateMismatch, "stateMismatch"},
		{service.ErrOIDCLoginNotAllowed, "userNotProvisioned"},
		{service.ErrOIDCDisabled, "disabled"},
		{service.ErrOIDCNotConfigured, "notConfigured"},
		{service.ErrOIDCUnknownProvider, "notConfigured"},
		{service.ErrOIDCPendingApproval, "pendingApproval"},
		{errors.New("anything else"), "unknown"},
	}
	for _, tc := range cases {
		if got := oidcErrorCode(tc.err); got != tc.want {
			t.Errorf("oidcErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A forwarded scheme is a string from a header, and only two schemes are
// a public origin. Anything else falls back to what the connection
// itself says, so a header cannot put an arbitrary scheme in front of
// every OPDS href and the redirect URI the admin panel displays.
func TestRequestOriginRefusesASchemeThatIsNotHTTP(t *testing.T) {
	for _, proto := range []string{"javascript", "file", "HTTPS ", "gopher"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "http://books.example.com/opds", nil)
		c.Request.Header.Set("X-Forwarded-Proto", proto)

		got := requestOrigin(c)
		if got != "http://books.example.com" && got != "https://books.example.com" {
			t.Errorf("X-Forwarded-Proto %q gave origin %q — only http and https are origins", proto, got)
		}
	}
}
