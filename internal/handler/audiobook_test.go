// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/tts"
)

// audiobookGetHandler builds the narrowest Handler BookAudiobookGet can
// run against: a real library service and audiobook repo over an empty
// schema, plus the audiobooks seam itself. Both cases below abort in
// bookScoped before that seam is touched, so a zero-value service is
// enough — it is supplied because app.go always supplies one, not to
// clear a nil guard, which the handlers no longer carry (#221).
func audiobookGetHandler(t *testing.T) (*Handler, *repo.BookRepo, *repo.LibraryRepo, *repo.BookAudiobookRepo) {
	t.Helper()
	d := repotest.New(t)
	books := repo.NewBookRepo(d)
	libs := repo.NewLibraryRepo(d)
	runs := repo.NewBookAudiobookRepo(d)
	h := &Handler{
		lib:           service.NewLibraryService(libs, books, service.LibraryServiceDeps{}, nil),
		books:         books,
		audiobookRepo: runs,
		audiobooks:    &service.AudiobookService{},
	}
	return h, books, libs, runs
}

// audiobookTestUser is a well-formed UUID because the per-user columns
// GetBook joins on are uuid-typed — a placeholder like "u1" fails at the
// driver and lands every case in the 500 arm.
const audiobookTestUser = "0a5d3b2c-1111-4111-8111-111111111111"

// audiobookGetCtx wires a GET for one book id, optionally with a session
// user attached the way auth.RequireAuth would have.
func audiobookGetCtx(t *testing.T, bookID string, withUser bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/books/"+bookID+"/audiobook", nil)
	c.Params = gin.Params{{Key: "id", Value: bookID}}
	if withUser {
		c.Request = c.Request.WithContext(
			auth.WithUser(c.Request.Context(), &model.User{ID: audiobookTestUser}))
	}
	return c, rec
}

// assertSingleResponse is the assertion this file exists for. A handler
// that writes twice keeps the status of the first write but appends both
// bodies, so gin reports a plausible code over an unparseable stream.
// Decoding the body as exactly one JSON value is what catches it.
func assertSingleResponse(t *testing.T, body string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("body is not a single JSON value: %v (body %q)", err, body)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("handler wrote more than one response: %q", body)
	}
}

// TestSettingsAudiobookUpdateResolvesTheKey covers the three-state key
// input per engine slot. The clear arm is the one that was missing: an
// admin who stored an ElevenLabs key had no way to remove it, because an
// empty key always meant "keep".
func TestSettingsAudiobookUpdateResolvesTheKey(t *testing.T) {
	ctx := context.Background()
	body := func(apiKey string, keySet bool) string {
		b, err := json.Marshal(audiobookSettingsRequest{
			Engine: string(tts.EngineElevenLabs),
			Engines: []audiobookEngineRequest{{
				ID: string(tts.EngineElevenLabs), APIKey: apiKey, KeySet: keySet,
			}},
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
		{"replace", "new-key", true, "new-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{appSettings: repo.NewAppSettingsRepo(repotest.New(t), nil)}
			seed := repo.DefaultAudiobookConfig()
			seed.ElevenLabs.APIKey = "stored-key"
			if err := h.appSettings.SetAudiobook(ctx, seed); err != nil {
				t.Fatalf("seed: %v", err)
			}

			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/settings/audiobook",
				strings.NewReader(body(tc.apiKey, tc.keySet)))
			c.Request.Header.Set("Content-Type", "application/json")

			h.SettingsAudiobookUpdate(c)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			got, err := h.appSettings.GetAudiobook(ctx)
			if err != nil {
				t.Fatalf("GetAudiobook: %v", err)
			}
			if got.ElevenLabs.APIKey != tc.want {
				t.Errorf("APIKey = %q, want %q", got.ElevenLabs.APIKey, tc.want)
			}
		})
	}
}

// TestBookAudiobookGetMissingBookIsA404 pins the branch the handler used
// to skip: the book lookup error went to the blank identifier, so a book
// that could not be loaded produced a zero-value Book that was then
// reported on as though it were real.
//
// The id is a well-formed UUID that no row carries — books.id is a uuid
// column, so a garbage string fails at the driver and reaches the 500
// arm instead of the missing-book one this test is about. The body is
// checked because both arms answer 404: only the book branch says the
// book is the thing that is missing.
func TestBookAudiobookGetMissingBookIsA404(t *testing.T) {
	h, _, _, _ := audiobookGetHandler(t)
	c, rec := audiobookGetCtx(t, "6f1c0f7e-0000-4000-8000-000000000000", true)

	h.bookScoped(h.BookAudiobookGet)(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "book not found") {
		t.Fatalf("body = %s, want the missing-book branch", rec.Body.String())
	}
	assertSingleResponse(t, rec.Body.String())
}

// TestBookAudiobookGetWithoutUserWritesOnlyThe401 is the regression the
// issue is about. requireUserID writes its own 401 and returns "", so
// calling it inline in the book lookup — after the run and coverage
// queries had already succeeded — left the handler free to carry on and
// write a 200 body after it. Two responses, and the status the client
// saw did not match the payload it got.
func TestBookAudiobookGetWithoutUserWritesOnlyThe401(t *testing.T) {
	h, books, libs, runs := audiobookGetHandler(t)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Audiobook Lib", "audiobook-lib", "/tmp/abl", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Narrated Book",
		Format:    "EPUB",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	// A real run, so the handler gets past both audiobook queries and
	// reaches the point where it used to derive the user id.
	if err := runs.Start(ctx, model.Audiobook{
		BookID: book.ID, Engine: "openai", Voice: "alloy", TotalChars: 10,
	}, []model.AudiobookSegment{{Seq: 0, ChapterTitle: "One", CharEnd: 10}}); err != nil {
		t.Fatalf("Start run: %v", err)
	}

	c, rec := audiobookGetCtx(t, book.ID, false)

	h.bookScoped(h.BookAudiobookGet)(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	assertSingleResponse(t, rec.Body.String())
}

// audiobookErrorResponse runs one error through the mapper and decodes
// the envelope it wrote. No database and no book: the mapper is a pure
// function of the error, and that is the whole surface under test.
func audiobookErrorResponse(t *testing.T, err error) (int, errorBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/books/x/audiobook", nil)

	h := &Handler{}
	h.writeAudiobookError(c, err)

	var body errorBody
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatalf("decode envelope: %v (body %q)", e, rec.Body.String())
	}
	return rec.Code, body
}

// TestAudiobookErrorDisabledIsA503WithTheDisabledCode pins the refusal an
// admin causes by turning the feature off. It used to fall through the
// mapper's default and reach the client as a bare 409 with no code, so
// the UI — which branches on AUDIOBOOKS_DISABLED — rendered a generic
// conflict toast instead of the disabled affordance.
func TestAudiobookErrorDisabledIsA503WithTheDisabledCode(t *testing.T) {
	status, body := audiobookErrorResponse(t, service.ErrAudiobooksDisabled)

	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %+v)", status, body)
	}
	if body.Code != CodeAudiobooksDisabled {
		t.Fatalf("code = %q, want %q", body.Code, CodeAudiobooksDisabled)
	}
}

// TestAudiobookErrorNotConfiguredIsA503WithTheDisabledCode is the other
// refusal, and it arrives wrapped: Preflight reads the settings through
// the default reader NewAudiobookService installs when none is wired, and
// returns "read audiobook settings: %w". The mapper has to match through
// the wrapping and answer with the sentinel's own message, because the
// wrapped one is a log line rather than something to show a user.
func TestAudiobookErrorNotConfiguredIsA503WithTheDisabledCode(t *testing.T) {
	wrapped := fmt.Errorf("read audiobook settings: %w", service.ErrAudiobooksNotConfigured)

	status, body := audiobookErrorResponse(t, wrapped)

	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %+v)", status, body)
	}
	if body.Code != CodeAudiobooksDisabled {
		t.Fatalf("code = %q, want %q", body.Code, CodeAudiobooksDisabled)
	}
	if body.Error != service.ErrAudiobooksNotConfigured.Error() {
		t.Fatalf("message = %q, want the unwrapped sentinel", body.Error)
	}
}

// TestAudiobookErrorDefaultsToA409 pins the arm the two cases above were
// added in front of. The default is not a catch-all for "unclassified":
// cancelling a finished run and retrying with nothing outstanding both
// land there, and both are genuinely conflicts — the caller asked for
// something the current state does not allow. Adding a mapped case must
// not take that away.
func TestAudiobookErrorDefaultsToA409(t *testing.T) {
	status, body := audiobookErrorResponse(t, errors.New("audiobook run is already ready"))

	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %+v)", status, body)
	}
	if body.Code != "" {
		t.Fatalf("code = %q, want none — a state conflict is not a coded case", body.Code)
	}
}
