// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
)

func TestSettingsConverterGetRendersTheRow(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{
		converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://converter:6070"},
	}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/converter", "")
	h.SettingsConverterGet(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got converterSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if !got.Enabled || got.BaseURL != "http://converter:6070" {
		t.Fatalf("DTO = %+v", got)
	}
}

func TestSettingsConverterUpdateStores(t *testing.T) {
	store := &fakeAppSettings{}
	h := &Handler{appSettings: store}

	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/converter",
		`{"enabled":true,"baseUrl":"http://converter:6070"}`)
	h.SettingsConverterUpdate(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !store.converter.Enabled || store.converter.BaseURL != "http://converter:6070" {
		t.Fatalf("stored = %+v", store.converter)
	}
}

// TestSettingsConverterUpdateRepoRefusalIsTheAdmins400 — validation
// refuses enabling without a URL; that is the admin's mistake to see,
// not a 500.
func TestSettingsConverterUpdateRepoRefusalIsTheAdmins400(t *testing.T) {
	store := &fakeAppSettings{setConverterErr: errors.New("the converter needs a base URL")}
	h := &Handler{appSettings: store}

	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/converter", `{"enabled":true}`)
	h.SettingsConverterUpdate(c)

	if httpStatus(c, rec) != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestSettingsConverterHealth — the card's three states. "Not configured"
// and "unreachable" are different answers (ADR-0033): the first means no
// probe was attempted at all.
func TestSettingsConverterHealth(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		h := &Handler{appSettings: &fakeAppSettings{}}
		c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/converter/health", "")
		h.SettingsConverterHealth(c)

		if httpStatus(c, rec) != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var got converterHealthDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Status != "not_configured" {
			t.Fatalf("Status = %q, want not_configured", got.Status)
		}
	})

	t.Run("reachable", func(t *testing.T) {
		sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("X-Converter-Version", "0.1.0")
			_, _ = w.Write([]byte("ok"))
		}))
		defer sidecar.Close()

		h := &Handler{appSettings: &fakeAppSettings{
			converter: repo.ConverterConfig{Enabled: true, BaseURL: sidecar.URL},
		}}
		c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/converter/health", "")
		h.SettingsConverterHealth(c)

		var got converterHealthDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
		}
		if got.Status != "ok" {
			t.Fatalf("Status = %q, want ok (body %s)", got.Status, rec.Body.String())
		}
		if got.Version != "0.1.0" {
			t.Fatalf("Version = %q, want the sidecar's header value", got.Version)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		// A closed server: the dial fails immediately.
		sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := sidecar.URL
		sidecar.Close()

		h := &Handler{appSettings: &fakeAppSettings{
			converter: repo.ConverterConfig{Enabled: true, BaseURL: url},
		}}
		c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/converter/health", "")
		h.SettingsConverterHealth(c)

		var got converterHealthDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
		}
		if got.Status != "unreachable" {
			t.Fatalf("Status = %q, want unreachable", got.Status)
		}
		if got.Error == "" {
			t.Fatal("unreachable must carry the dial error, verbatim")
		}
	})
}
