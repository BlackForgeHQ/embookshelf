// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// fakeProviderSettings satisfies the narrow store the
// ProviderSettingsService depends on, so the admin surface can be
// exercised without a database.
type fakeProviderSettings struct {
	rows []repo.ProviderSetting
}

func (f *fakeProviderSettings) List(context.Context) ([]repo.ProviderSetting, error) {
	return f.rows, nil
}

func (f *fakeProviderSettings) AllConfigs(context.Context) (map[string]json.RawMessage, error) {
	return map[string]json.RawMessage{}, nil
}

func (f *fakeProviderSettings) EnabledIDs(context.Context) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (f *fakeProviderSettings) SetConfig(context.Context, string, json.RawMessage) error { return nil }
func (f *fakeProviderSettings) SetEnabled(context.Context, string, bool) error           { return nil }
func (f *fakeProviderSettings) SetPriority(context.Context, string, *int) error          { return nil }
func (f *fakeProviderSettings) RecordSuccess(context.Context, string) error              { return nil }
func (f *fakeProviderSettings) RecordError(context.Context, string, string) error        { return nil }

// TestSettingsProvidersListReturnsCatalog covers the admin provider
// surface, which shipped returning 500 because ProviderCfg was declared
// in Deps, copied to the Handler, and never set by the composition root.
// Nothing caught it: the field has no nil guard, and no test in the
// package had ever constructed a Handler.
func TestSettingsProvidersListReturnsCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &Handler{
		providerCfg: service.NewProviderSettingsService(
			nil, // no live provider adapters needed to render the catalog
			&fakeProviderSettings{rows: []repo.ProviderSetting{
				{ID: "google_books", Enabled: true},
			}},
			nil,
		),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/providers", nil)

	h.SettingsProvidersList(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Providers []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if len(body.Providers) == 0 {
		t.Fatal("providers list is empty — the catalog should always render")
	}
	var found bool
	for _, p := range body.Providers {
		if p.ID == "google_books" {
			found = true
			if !p.Enabled {
				t.Error("google_books reported disabled despite an enabled row")
			}
		}
	}
	if !found {
		t.Errorf("google_books missing from %+v", body.Providers)
	}
}
