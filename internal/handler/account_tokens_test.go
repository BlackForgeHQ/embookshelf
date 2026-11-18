// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// fakeTokenManager is an apiTokenManager that records what it was asked
// and answers with injected results. The service's own rules have their
// own tests; these check the wire.
type fakeTokenManager struct {
	createErr  error
	createdTTL time.Duration
	created    model.APIToken
	list       []model.APIToken
	all        []repo.APITokenWithOwner
	revokeErr  error
	revokedBy  string
	revokedID  string
	users      map[string]model.User
	resolved   int
}

func (f *fakeTokenManager) Create(_ context.Context, userID, name string, ttl time.Duration) (string, model.APIToken, error) {
	if f.createErr != nil {
		return "", model.APIToken{}, f.createErr
	}
	f.createdTTL = ttl
	f.created = model.APIToken{
		ID: "t1", UserID: userID, Name: name, Prefix: "abcd1234",
		CreatedAt: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
	}
	return "ebk_abcd1234rest", f.created, nil
}

func (f *fakeTokenManager) List(context.Context, string) ([]model.APIToken, error) {
	return f.list, nil
}

func (f *fakeTokenManager) ListAll(context.Context) ([]repo.APITokenWithOwner, error) {
	return f.all, nil
}

func (f *fakeTokenManager) Revoke(_ context.Context, userID, id string) error {
	f.revokedBy, f.revokedID = userID, id
	return f.revokeErr
}

func (f *fakeTokenManager) RevokeAny(_ context.Context, id string) error {
	f.revokedID = id
	return f.revokeErr
}

func (f *fakeTokenManager) UserByToken(_ context.Context, raw string) (model.User, error) {
	u, ok := f.users[raw]
	if !ok {
		return model.User{}, auth.ErrInvalidToken
	}
	f.resolved++
	return u, nil
}

func TestAccountTokenCreateReturnsThePlaintextOnce(t *testing.T) {
	tokens := &fakeTokenManager{}
	h := &Handler{apiTokens: tokens}
	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/account/tokens", `{"name":"laptop","expiresInDays":30}`)

	h.AccountTokenCreate(c, "u1")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body)
	}
	var body struct {
		Token string      `json:"token"`
		Meta  apiTokenDTO `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Token != "ebk_abcd1234rest" {
		t.Errorf("token = %q", body.Token)
	}
	if body.Meta.Prefix != "ebk_abcd1234" {
		t.Errorf("display prefix = %q, want the ebk_-prefixed form", body.Meta.Prefix)
	}
	if body.Meta.ExpiresAt != nil || body.Meta.LastUsedAt != nil {
		t.Errorf("nil times did not encode as null: %+v", body.Meta)
	}
	if tokens.createdTTL != 30*24*time.Hour {
		t.Errorf("ttl = %v, want 30 days", tokens.createdTTL)
	}
}

func TestAccountTokenCreateRefusals(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		err      error
		status   int
		wantCode string
	}{
		{"mcp off", `{"name":"x"}`, service.ErrMCPDisabled, http.StatusServiceUnavailable, CodeMCPDisabled},
		{"bad name", `{"name":""}`, service.ErrAPITokenName, http.StatusBadRequest, ""},
		{"negative expiry", `{"name":"x","expiresInDays":-1}`, nil, http.StatusBadRequest, ""},
		{"absurd expiry", `{"name":"x","expiresInDays":3651}`, nil, http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{apiTokens: &fakeTokenManager{createErr: tc.err}}
			c, rec := settingsCtx(t, http.MethodPost, "/api/v1/account/tokens", tc.body)
			h.AccountTokenCreate(c, "u1")
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("body %s lacks code %s", rec.Body, tc.wantCode)
			}
		})
	}
}

func TestAccountTokenRevokeIsScopedAndMaps404(t *testing.T) {
	tokens := &fakeTokenManager{}
	h := &Handler{apiTokens: tokens}
	c, rec := settingsCtx(t, http.MethodDelete, "/api/v1/account/tokens/t9", "")
	c.AddParam("id", "t9")

	h.AccountTokenRevoke(c, "u1")
	if got := httpStatus(c, rec); got != http.StatusNoContent {
		t.Fatalf("status %d, want 204", got)
	}
	if tokens.revokedBy != "u1" || tokens.revokedID != "t9" {
		t.Fatalf("revoked (%q, %q), want (u1, t9)", tokens.revokedBy, tokens.revokedID)
	}

	tokens.revokeErr = repo.ErrNotFound
	c, rec = settingsCtx(t, http.MethodDelete, "/api/v1/account/tokens/t9", "")
	c.AddParam("id", "t9")
	h.AccountTokenRevoke(c, "u1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-found revoke: status %d, want 404", rec.Code)
	}
}

func TestAccountTokensListNeverCarriesASecret(t *testing.T) {
	used := time.Date(2026, 11, 18, 13, 0, 0, 0, time.UTC)
	h := &Handler{apiTokens: &fakeTokenManager{list: []model.APIToken{
		{ID: "t1", Name: "laptop", Prefix: "abcd1234", CreatedAt: used, LastUsedAt: &used},
	}}}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/account/tokens", "")

	h.AccountTokensList(c, "u1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("list response carries a token field: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"lastUsedAt":"2026-11-18T13:00:00Z"`) {
		t.Fatalf("lastUsedAt missing: %s", rec.Body)
	}
}

func TestSettingsMCPPutRepublishesTheSwitch(t *testing.T) {
	sw := service.NewMCPSwitch(false)
	store := &fakeAppSettings{}
	h := &Handler{appSettings: store, mcp: sw}

	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/mcp", `{"enabled":true}`)
	h.SettingsMCPUpdate(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !store.mcp.Enabled {
		t.Fatal("row not saved")
	}
	if !sw.Enabled() {
		t.Fatal("runtime switch still off after enabling")
	}

	c, _ = settingsCtx(t, http.MethodPut, "/api/v1/settings/mcp", `{"enabled":false}`)
	h.SettingsMCPUpdate(c)
	if sw.Enabled() {
		t.Fatal("runtime switch still on after disabling")
	}
}

func TestAppConfigReportsMCP(t *testing.T) {
	h := &Handler{mcp: service.NewMCPSwitch(true)}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/config", "")
	h.AppConfig(c)
	if !strings.Contains(rec.Body.String(), `"mcpEnabled":true`) {
		t.Fatalf("config %s lacks mcpEnabled:true", rec.Body)
	}
}

func TestSettingsMCPTokensListCarriesTheOwner(t *testing.T) {
	h := &Handler{apiTokens: &fakeTokenManager{all: []repo.APITokenWithOwner{{
		APIToken:   model.APIToken{ID: "t1", UserID: "u2", Name: "ci", Prefix: "zzzz9999"},
		OwnerEmail: "bob@example.com",
		OwnerName:  "Bob",
	}}}}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/mcp/tokens", "")

	h.SettingsMCPTokensList(c)

	want := `"owner":{"id":"u2","email":"bob@example.com","name":"Bob"}`
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("body %s lacks %s", rec.Body, want)
	}
}
