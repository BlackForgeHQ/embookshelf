// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
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

// --- bulk conversion ------------------------------------------------------

type fakeConversionStore struct {
	candidates []repo.ConversionCandidate
	started    []string
}

func (f *fakeConversionStore) ListConversionCandidates(context.Context) ([]repo.ConversionCandidate, error) {
	return f.candidates, nil
}

func (f *fakeConversionStore) Start(_ context.Context, bookID string) error {
	f.started = append(f.started, bookID)
	return nil
}

func TestSettingsConverterCoverageDerivesCandidates(t *testing.T) {
	h := &Handler{renditions: &fakeRenditions{coverage: repo.ConversionCoverage{
		Total: 10, Ready: 5, Converting: 2, Failed: 1, Unconverted: 2,
	}}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/converter/coverage", "")
	h.SettingsConverterCoverage(c)

	var got converterCoverageDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if got.Candidates != 3 {
		t.Fatalf("Candidates = %d, want unconverted+failed = 3", got.Candidates)
	}
	if got.Converting != 2 || got.Ready != 5 || got.Total != 10 {
		t.Fatalf("DTO = %+v", got)
	}
}

// TestSettingsConverterRunStartsRowsAndEnqueues — every candidate's row
// goes pending before its job, so the first coverage poll already
// counts the run as converting.
func TestSettingsConverterRunStartsRowsAndEnqueues(t *testing.T) {
	store := &fakeConversionStore{candidates: []repo.ConversionCandidate{
		{BookID: "b1"}, {BookID: "b2"},
	}}
	q := &captureQueue{}
	h := &Handler{
		conversionRunner: service.NewConversionRunner(store, q),
		appSettings: &fakeAppSettings{
			converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"},
		},
	}

	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/settings/converter/run", "")
	h.SettingsConverterRun(c)

	if httpStatus(c, rec) != http.StatusAccepted {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if len(store.started) != 2 || len(q.enqueued) != 2 {
		t.Fatalf("started %v, enqueued %d", store.started, len(q.enqueued))
	}
	var got struct {
		Queued int `json:"queued"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Queued != 2 {
		t.Fatalf("body = %s (err %v)", rec.Body.String(), err)
	}
}

// TestSettingsConverterRunRefusesWhenNotConfigured — same gate as the
// per-book button: the loud answer beats a thousand failing jobs.
func TestSettingsConverterRunRefusesWhenNotConfigured(t *testing.T) {
	h := &Handler{
		conversionRunner: service.NewConversionRunner(&fakeConversionStore{}, &captureQueue{}),
		appSettings:      &fakeAppSettings{},
	}
	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/settings/converter/run", "")
	h.SettingsConverterRun(c)

	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "converter extension is not configured") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// TestRequireConverter — the converter gate mirrors requireQueue: one
// place owns the 503 + code + refusal string, so a route cannot restate
// the predicate and drift (#298 — the drift that once nil-dereferenced
// Send-to-Kindle on the queue seam).
func TestRequireConverter(t *testing.T) {
	t.Run("configured passes the config through", func(t *testing.T) {
		h := &Handler{appSettings: &fakeAppSettings{
			converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://converter:6070"},
		}}
		c, rec := settingsCtx(t, http.MethodPost, "/x", "")
		cfg, ok := h.requireConverter(c)
		if !ok || cfg.BaseURL != "http://converter:6070" {
			t.Fatalf("cfg = %+v ok = %v, want the configured row", cfg, ok)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("wrote %s on the success path", rec.Body.String())
		}
	})

	t.Run("not configured owns the refusal", func(t *testing.T) {
		h := &Handler{appSettings: &fakeAppSettings{}}
		c, rec := settingsCtx(t, http.MethodPost, "/x", "")
		if _, ok := h.requireConverter(c); ok {
			t.Fatal("ok = true for an unconfigured converter")
		}
		if httpStatus(c, rec) != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Code != CodeConverterDisabled || body.Error != "converter extension is not configured" {
			t.Fatalf("body = %+v", body)
		}
	})

	t.Run("settings read failure is a 500", func(t *testing.T) {
		h := &Handler{appSettings: &fakeAppSettings{converterErr: errors.New("db down")}}
		c, rec := settingsCtx(t, http.MethodPost, "/x", "")
		if _, ok := h.requireConverter(c); ok {
			t.Fatal("ok = true on a settings read failure")
		}
		if httpStatus(c, rec) != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}
