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
