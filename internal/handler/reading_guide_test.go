// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

func guideCtx(t *testing.T, method, target string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	c.Request = r
	return c, rec
}

// TestReadingGuideSettingsDTOHidesKey is the important property of the
// admin surface: the stored API key must never travel to the browser,
// only a flag saying one exists. Mirrors how the SMTP password is
// handled.
func TestReadingGuideSettingsDTOHidesKey(t *testing.T) {
	dto := readingGuideSettingsDTO{
		Enabled: true, BaseURL: "https://api.openai.com/v1",
		Model: "gpt-4o-mini", KeySet: true,
		Language: "en", TextCap: 48_000,
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "apiKey") {
		t.Fatalf("apiKey present in the response body: %s", raw)
	}
	if !strings.Contains(string(raw), `"keySet":true`) {
		t.Errorf("keySet missing: %s", raw)
	}
}

// TestBookGuideEditRejectsEmpty — an all-blank edit would blank the guide
// while marking it hand-written, which then excludes the book from every
// future run. Refuse it.
func TestBookGuideEditRejectsEmpty(t *testing.T) {
	h := &Handler{}
	c, rec := guideCtx(t, http.MethodPut, "/api/v1/books/b1/guide",
		`{"about":"  ","audience":"","notFor":"","problems":""}`)
	c.Params = gin.Params{{Key: "id", Value: "b1"}}
	c.Request = c.Request.WithContext(
		auth.WithUser(c.Request.Context(), &model.User{ID: "u1"}))

	// Driven as a body rather than through the seam: the book-scoped seam
	// has already done the resolving, so the empty-guide rule is
	// exercisable without a catalog behind it at all.
	h.BookGuideEdit(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestReadingGuideJobArgsCarryTheBook pins the payload the endpoint
// queues: BookID only, so the worker re-reads the row and a metadata
// edit between enqueue and dispatch reaches the prompt.
func TestReadingGuideJobArgsCarryTheBook(t *testing.T) {
	a := jobs.ReadingGuideArgs{BookID: "b1"}
	if a.Kind() != "guide.generate" {
		t.Fatalf("Kind = %q", a.Kind())
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"book_id":"b1"`) {
		t.Fatalf("payload = %s", raw)
	}
}

// storedReadingGuide is a configured READING_GUIDE row with a key in it.
func storedReadingGuide() repo.ReadingGuideConfig {
	return repo.ReadingGuideConfig{
		Enabled: true,
		BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini",
		APIKey: "stored-key", Language: "en", TextCap: 48_000,
	}
}

// TestSettingsReadingGuideGetNeverReturnsTheKey — the LLM key is the one
// field on this panel that costs money if it leaks, and the GET is
// allowed to say only that one exists.
func TestSettingsReadingGuideGetNeverReturnsTheKey(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{guide: storedReadingGuide()}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/settings/reading-guide", "")
	h.SettingsReadingGuideGet(c)

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "stored-key") {
		t.Fatalf("the API key travelled to the client: %s", rec.Body.String())
	}
	var got readingGuideSettingsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if !got.KeySet || got.Model != "gpt-4o-mini" || got.TextCap != 48_000 {
		t.Errorf("round trip lost fields: %+v", got)
	}
}

// TestSettingsReadingGuideUpdateResolvesTheKey covers the three-state key
// input. The clear arm is the one that was missing: an admin who stored an
// LLM key had no way to remove it, because an empty key always meant
// "keep".
//
// Runs against the fake store rather than a live database. It used to
// need Postgres, which is the reason the other four settings panels had
// no endpoint test at all — the same rule, five times over, testable only
// where someone had already paid for a schema.
func TestSettingsReadingGuideUpdateResolvesTheKey(t *testing.T) {
	stored := storedReadingGuide()
	body := func(apiKey string, keySet bool) string {
		b, err := json.Marshal(readingGuideSettingsDTO{
			Enabled: true, BaseURL: stored.BaseURL, Model: stored.Model,
			APIKey: apiKey, KeySet: keySet,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}

	cases := []struct {
		name   string
		apiKey string
		keySet bool
		want   string
	}{
		{"clear", "", false, ""},
		{"keep", "", true, "stored-key"},
		{"replace", " new-key ", true, "new-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAppSettings{guide: stored}
			h := &Handler{appSettings: store}

			c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/reading-guide",
				body(tc.apiKey, tc.keySet))
			h.SettingsReadingGuideUpdate(c)

			if httpStatus(c, rec) != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			if store.guideWrites != 1 {
				t.Fatalf("row written %d times, want 1", store.guideWrites)
			}
			if store.guide.APIKey != tc.want {
				t.Errorf("APIKey = %q, want %q", store.guide.APIKey, tc.want)
			}
			if strings.Contains(rec.Body.String(), "key") && strings.Contains(rec.Body.String(), tc.want) && tc.want != "" {
				t.Errorf("the PUT response echoed the key back: %s", rec.Body.String())
			}
		})
	}
}

// TestSettingsReadingGuideUpdateRefusalIsA400 — the row refuses being
// enabled without an endpoint to call, which is the admin's to fix and
// not a 500.
func TestSettingsReadingGuideUpdateRefusalIsA400(t *testing.T) {
	store := &fakeAppSettings{guide: storedReadingGuide(), setGuideErr: errBoom}
	h := &Handler{appSettings: store}

	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/reading-guide",
		`{"enabled":true,"baseUrl":"","model":""}`)
	h.SettingsReadingGuideUpdate(c)

	if httpStatus(c, rec) != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestSettingsReadingGuideTestNeedsAnEndpoint — the connection probe
// refuses before it dials when there is nothing configured to dial.
func TestSettingsReadingGuideTestNeedsAnEndpoint(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{guide: repo.ReadingGuideConfig{Enabled: true}}}

	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/settings/reading-guide/test", "")
	h.SettingsReadingGuideTest(c)

	if httpStatus(c, rec) != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestBookGuideGenerateIsA503WhenGuidesAreOff — the book-scoped generate
// gates on the admin row, and the UI switches on the catalogued code to
// explain the button that did nothing.
func TestBookGuideGenerateIsA503WhenGuidesAreOff(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{guide: repo.ReadingGuideConfig{Enabled: false}}}

	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
	h.BookGuideGenerate(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), CodeGuidesDisabled) {
		t.Errorf("body carries no %s code: %s", CodeGuidesDisabled, rec.Body.String())
	}
}

// TestGuidesDisabledCodeIsDeclared — the UI switches on this string, so
// it has to be in the catalog the error-envelope tests enumerate.
func TestGuidesDisabledCodeIsDeclared(t *testing.T) {
	for _, code := range AllErrorCodes {
		if code == CodeGuidesDisabled {
			return
		}
	}
	t.Fatalf("%q missing from AllErrorCodes", CodeGuidesDisabled)
}
