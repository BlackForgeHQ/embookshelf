// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// fakeReadingGuideStore is the readingGuideStore fake: get-by-book and
// save-edit, driven against maps rather than Postgres — same reasoning
// as fakeRenditions next door.
type fakeReadingGuideStore struct {
	row     model.ReadingGuide
	missing bool
	// saveMissing answers SaveEdit's own not-found: a book with no guide
	// row yet has nothing to edit.
	saveMissing bool
	saved       *model.ReadingGuideText
}

func (f *fakeReadingGuideStore) GetByBookID(context.Context, string) (model.ReadingGuide, error) {
	if f.missing {
		return model.ReadingGuide{}, repo.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeReadingGuideStore) SaveEdit(_ context.Context, _ string, t model.ReadingGuideText) error {
	if f.saveMissing {
		return repo.ErrNotFound
	}
	f.saved = &t
	f.row.ReadingGuideText = t
	f.row.EditedByUser = true
	return nil
}

var _ readingGuideStore = (*fakeReadingGuideStore)(nil)

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

// TestBookGuideGetReturnsTheStoredGuide pins the read path's success
// shape — untested before the seam existed, because reaching it meant a
// real BookReadingGuideRepo over Postgres.
func TestBookGuideGetReturnsTheStoredGuide(t *testing.T) {
	generatedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h := &Handler{guides: &fakeReadingGuideStore{row: model.ReadingGuide{
		BookID: "b1",
		ReadingGuideText: model.ReadingGuideText{
			About: "A quiet heist novel.", Audience: "fans of slow burns",
			NotFor: "action readers", Problems: "no problems",
		},
		SourceKind:  model.GuideSourceFullText,
		Model:       "gpt-4o-mini",
		Language:    "en",
		GeneratedAt: generatedAt,
	}}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/guide", "")
	h.BookGuideGet(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Guide readingGuideDTO `json:"guide"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if got.Guide.About != "A quiet heist novel." || got.Guide.SourceKind != string(model.GuideSourceFullText) {
		t.Errorf("guide = %+v, want the stored row's text and provenance", got.Guide)
	}
	if got.Guide.GeneratedAt != generatedAt.Format(time.RFC3339) {
		t.Errorf("generatedAt = %q, want %q", got.Guide.GeneratedAt, generatedAt.Format(time.RFC3339))
	}
}

// TestBookGuideGetIsNotFoundForAVirginBook — 404, not an empty guide:
// there is no such thing as an empty stored guide, so the two cases
// cannot be confused on the wire.
func TestBookGuideGetIsNotFoundForAVirginBook(t *testing.T) {
	h := &Handler{guides: &fakeReadingGuideStore{missing: true}}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/guide", "")
	h.BookGuideGet(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no reading guide for this book yet") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestBookGuideGetNilStoreAnswers503 pins the acceptance criterion
// directly: a Handler built with no reading-guide repo answers the
// documented unavailable response rather than reaching into a nil
// interface, which is exactly what the concrete field this seam replaces
// let happen unguarded.
func TestBookGuideGetNilStoreAnswers503(t *testing.T) {
	h := &Handler{}

	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/guide", "")
	h.BookGuideGet(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), guidesUnavailableMsg) {
		t.Errorf("body = %s, want %q", rec.Body.String(), guidesUnavailableMsg)
	}
}

// TestBookGuideEditSavesThenReloads pins the write path's success shape:
// SaveEdit runs, then the row is reloaded so the response carries what
// is now stored — including EditedByUser flipping true.
func TestBookGuideEditSavesThenReloads(t *testing.T) {
	store := &fakeReadingGuideStore{row: model.ReadingGuide{
		BookID: "b1", SourceKind: model.GuideSourceFullText, Model: "gpt-4o-mini",
	}}
	h := &Handler{guides: store}
	c, rec := guideCtx(t, http.MethodPut, "/api/v1/books/b1/guide",
		`{"about":" hand-written ","audience":"","notFor":"","problems":""}`)
	c.Params = gin.Params{{Key: "id", Value: "b1"}}
	c.Request = c.Request.WithContext(
		auth.WithUser(c.Request.Context(), &model.User{ID: "u1"}))

	h.BookGuideEdit(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if store.saved == nil || store.saved.About != "hand-written" {
		t.Fatalf("SaveEdit got %+v, want the trimmed text", store.saved)
	}
	var got struct {
		Guide readingGuideDTO `json:"guide"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if got.Guide.About != "hand-written" || !got.Guide.EditedByUser {
		t.Errorf("guide = %+v, want the reloaded, now hand-edited row", got.Guide)
	}
}

// TestBookGuideEditIsNotFoundWithNoGuideToEdit — SaveEdit's own
// not-found, distinct from the empty-body 400: a well-formed edit for a
// book that has never had a guide generated.
func TestBookGuideEditIsNotFoundWithNoGuideToEdit(t *testing.T) {
	h := &Handler{guides: &fakeReadingGuideStore{saveMissing: true}}
	c, rec := guideCtx(t, http.MethodPut, "/api/v1/books/b1/guide",
		`{"about":"hand-written","audience":"","notFor":"","problems":""}`)
	c.Params = gin.Params{{Key: "id", Value: "b1"}}
	c.Request = c.Request.WithContext(
		auth.WithUser(c.Request.Context(), &model.User{ID: "u1"}))

	h.BookGuideEdit(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no reading guide for this book yet") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestBookGuideEditNilStoreAnswers503 — the empty-body 400 in
// TestBookGuideEditRejectsEmpty already proves that check runs with no
// store at all; this proves a well-formed edit past that check answers
// the documented unavailable response rather than a nil dereference.
func TestBookGuideEditNilStoreAnswers503(t *testing.T) {
	h := &Handler{}
	c, rec := guideCtx(t, http.MethodPut, "/api/v1/books/b1/guide",
		`{"about":"hand-written","audience":"","notFor":"","problems":""}`)
	c.Params = gin.Params{{Key: "id", Value: "b1"}}
	c.Request = c.Request.WithContext(
		auth.WithUser(c.Request.Context(), &model.User{ID: "u1"}))

	h.BookGuideEdit(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), guidesUnavailableMsg) {
		t.Errorf("body = %s, want %q", rec.Body.String(), guidesUnavailableMsg)
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

// TestBookGuideGenerateNilStoreAnswersSameCodeAsDisabled — a Handler
// with the admin row on but no reading-guide repo wired answers the same
// CodeGuidesDisabled refusal as one the admin switched off, rather than
// enqueueing work no GET or PUT could ever read back. Pins the
// acceptance criterion for the generate leg: availability there folds
// the store's presence into the same spec field the admin row already
// used.
func TestBookGuideGenerateNilStoreAnswersSameCodeAsDisabled(t *testing.T) {
	h := &Handler{appSettings: &fakeAppSettings{guide: repo.ReadingGuideConfig{Enabled: true}}}

	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
	h.BookGuideGenerate(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1"}})

	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), CodeGuidesDisabled) {
		t.Errorf("body carries no %s code: %s", CodeGuidesDisabled, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), guidesDisabledMsg) {
		t.Errorf("body = %s, want %q", rec.Body.String(), guidesDisabledMsg)
	}
}

// TestBookGuideGenerateNonConvertibleFormatReachesTheQueue pins the
// guide's own format rule against the shared chain's built-in one: a
// non-Convertible book (EPUB, native text) is not refused the way
// markdown's and the EPUB's own generate routes refuse it — a
// metadata-only guide asks nothing of the converter, so it reaches the
// queue like any other format.
func TestBookGuideGenerateNonConvertibleFormatReachesTheQueue(t *testing.T) {
	q := &captureQueue{}
	h := &Handler{
		guides:      &fakeReadingGuideStore{missing: true},
		appSettings: &fakeAppSettings{guide: repo.ReadingGuideConfig{Enabled: true}},
		queue:       q,
	}

	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
	h.BookGuideGenerate(c, bookScope{UserID: "u1", Book: model.Book{ID: "b1", Format: "EPUB"}})

	if httpStatus(c, rec) != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	if len(q.enqueued) != 1 {
		t.Fatalf("enqueued = %d jobs, want 1", len(q.enqueued))
	}
	if _, ok := q.enqueued[0].(jobs.ReadingGuideArgs); !ok {
		t.Fatalf("enqueued %T, want jobs.ReadingGuideArgs", q.enqueued[0])
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
