// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/repo"
)

// TestSettingsForwardAuthGetReturnsTheRow — the panel has to render what
// is actually enforced, CIDR list included, or an operator debugging a
// proxy is reading a different config from the middleware.
func TestSettingsForwardAuthGetReturnsTheRow(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{forwardAuth: repo.ForwardAuthConfig{
		Enabled:           true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"},
		Headers:           repo.ForwardAuthHeaders{User: "Remote-User", Email: "Remote-Email"},
		LogoutURL:         "https://auth.example.com/logout",
		HideLocalLogin:    true,
	}}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/forward-auth", "")
	h.SettingsForwardAuthGet(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got forwardAuthSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if !got.Enabled || !got.HideLocalLogin {
		t.Errorf("flags lost: %+v", got)
	}
	if len(got.TrustedProxyCIDRs) != 1 || got.TrustedProxyCIDRs[0] != "10.0.0.0/8" {
		t.Errorf("trusted CIDRs = %v", got.TrustedProxyCIDRs)
	}
	if got.Headers.User != "Remote-User" {
		t.Errorf("headers lost: %+v", got.Headers)
	}
}

// TestSettingsForwardAuthUpdatePublishesToTheHolder is the property that
// makes the panel worth having: a save has to reach the running
// middleware, not just the row. Without the hot-swap an operator locks
// themselves out and the fix needs a restart.
func TestSettingsForwardAuthUpdatePublishesToTheHolder(t *testing.T) {
	store := &fakeAppSettings{}
	holder := auth.NewForwardAuthHolder(nil)
	h := &Handler{appSettings: store, fwdAuthHolder: holder}

	body := `{"enabled":true,"trustedProxyCIDRs":["10.0.0.0/8"],
		"headers":{"user":"Remote-User","email":"Remote-Email","name":"Remote-Name"},
		"hideLocalLogin":true}`
	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/forward-auth", body)
	h.SettingsForwardAuthUpdate(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !store.forwardAuth.Enabled || len(store.forwardAuth.TrustedProxyCIDRs) != 1 {
		t.Fatalf("row not persisted: %+v", store.forwardAuth)
	}
	live := holder.Get()
	if live == nil {
		t.Fatal("the runtime holder was never updated — the save would need a restart to take effect")
	}
}

// TestSettingsForwardAuthUpdateSplitsRefusalsFromFailures — every
// validation sentinel the row can return is the operator's to fix and
// must arrive as a 400 with its message. A missing arm here is how a
// typo'd CIDR turns into an unexplained "internal error".
func TestSettingsForwardAuthUpdateSplitsRefusalsFromFailures(t *testing.T) {
	const body = `{"enabled":true,"trustedProxyCIDRs":["10.0.0.0/8"],
		"headers":{"user":"Remote-User"}}`

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"enabled without a CIDR", repo.ErrForwardAuthEnabledWithoutCIDR, http.StatusBadRequest},
		{"malformed CIDR", fmt.Errorf("%w: %q", repo.ErrForwardAuthInvalidCIDR, "10.0.0/8"), http.StatusBadRequest},
		{"bad header name", fmt.Errorf("%w: user", repo.ErrForwardAuthInvalidHeader), http.StatusBadRequest},
		{"bad logout URL", repo.ErrForwardAuthInvalidLogoutURL, http.StatusBadRequest},
		{"anything else", errBoom, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAppSettings{setFwdErr: tc.err}
			h := &Handler{appSettings: store}

			c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/forward-auth", body)
			h.SettingsForwardAuthUpdate(c)

			if httpStatus(c, rec) != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "forward_auth") {
				t.Errorf("the sentinel's message did not reach the operator: %s", rec.Body.String())
			}
		})
	}
}
