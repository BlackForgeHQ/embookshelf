// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/mcpserver"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// mcpEngine is the real engine with MCP wired: a fake token store that
// knows one token, and the real MCP server over empty deps (initialize
// and tools/list need none).
func mcpEngine(t *testing.T, enabled bool) (*gin.Engine, *fakeTokenManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tokens := &fakeTokenManager{users: map[string]model.User{
		"ebk_good": {ID: "u1", Role: model.RoleUser, Status: model.UserStatusActive},
	}}
	h := &Handler{
		cfg:       config.Config{AllowedOrigins: []string{"http://localhost:5173"}},
		mcp:       service.NewMCPSwitch(enabled),
		apiTokens: tokens,
		mcpServer: mcpserver.Handler(mcpserver.New(mcpserver.Deps{Version: "test"})),
	}
	return h.Engine(), tokens
}

func mcpPost(engine *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpInitialize))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, r)
	return rec
}

func TestMCPServesAValidTokenWithoutAnOrigin(t *testing.T) {
	engine, tokens := mcpEngine(t, true)

	rec := mcpPost(engine, map[string]string{"Authorization": "Bearer ebk_good"})

	if rec.Code != http.StatusOK {
		t.Fatalf("initialize: status %d, want 200: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"name":"embookshelf"`) {
		t.Fatalf("initialize answered %s", rec.Body)
	}
	if tokens.resolved != 1 {
		t.Fatalf("token resolved %d times, want 1", tokens.resolved)
	}
}

func TestMCPDisabledAnswersBeforeTouchingTokens(t *testing.T) {
	engine, tokens := mcpEngine(t, false)

	rec := mcpPost(engine, map[string]string{"Authorization": "Bearer ebk_good"})

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), CodeMCPDisabled) {
		t.Fatalf("disabled: status %d body %s, want 503 %s", rec.Code, rec.Body, CodeMCPDisabled)
	}
	if tokens.resolved != 0 {
		t.Fatal("a disabled instance resolved a token")
	}
}

func TestMCPRefusals(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"no token", nil, http.StatusUnauthorized},
		{"unknown token", map[string]string{"Authorization": "Bearer ebk_bad"}, http.StatusUnauthorized},
		{"cookie only", map[string]string{"Cookie": "embookshelf_session=live"}, http.StatusUnauthorized},
		{"foreign origin", map[string]string{"Authorization": "Bearer ebk_good", "Origin": "https://evil.example"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, tokens := mcpEngine(t, true)
			rec := mcpPost(engine, tc.headers)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.status == http.StatusForbidden && tokens.resolved != 0 {
				t.Fatal("a foreign origin got as far as the token lookup")
			}
		})
	}
}

func TestMCPAcceptsItsOwnAndTrustedOrigins(t *testing.T) {
	for _, origin := range []string{"http://example.com", "http://localhost:5173"} {
		engine, _ := mcpEngine(t, true)
		// httptest requests carry Host example.com.
		rec := mcpPost(engine, map[string]string{"Authorization": "Bearer ebk_good", "Origin": origin})
		if rec.Code != http.StatusOK {
			t.Fatalf("origin %s: status %d, want 200: %s", origin, rec.Code, rec.Body)
		}
	}
}

// The CSRF exemption is /mcp exactly: a cookie route still demands an
// Origin, and so does a path that merely starts with /mcp. (/mcp/ is
// gin's trailing-slash redirect back onto the bearer-only route.)
func TestCSRFExemptionIsOnlyMCP(t *testing.T) {
	engine, _ := mcpEngine(t, true)
	for _, path := range []string{"/api/v1/account/tokens", "/mcpx", "/mcp/x"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "missing origin") {
			t.Errorf("POST %s without Origin: status %d body %s, want 403 missing origin", path, rec.Code, rec.Body)
		}
	}
}

func TestMCPFailsClosedWithoutATokenService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{
		cfg:       config.Config{AllowedOrigins: []string{"http://localhost:5173"}},
		mcp:       service.NewMCPSwitch(true),
		mcpServer: mcpserver.Handler(mcpserver.New(mcpserver.Deps{})),
	}
	rec := mcpPost(h.Engine(), map[string]string{"Authorization": "Bearer ebk_good"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token service: status %d, want 401", rec.Code)
	}
}
