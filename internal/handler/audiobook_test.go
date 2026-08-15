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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
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
		lib:        service.NewLibraryService(libs, books, service.LibraryServiceDeps{}, nil),
		books:      books,
		audiobooks: &service.AudiobookService{},
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
		b, err := json.Marshal(audiobookSettingsDTO{
			Engine: string(tts.EngineElevenLabs),
			Engines: []audiobookEngineDTO{{
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
	if _, err := runs.Start(ctx, model.Audiobook{
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

// unknownEngineError is the refusal a settings row produces by naming an
// engine the catalog does not have. Taken from SelectedEngine rather than
// hand-written: a mapper case is only worth anything if it matches the
// error the production path actually returns.
func unknownEngineError(t *testing.T, id string) error {
	t.Helper()
	_, _, err := repo.AudiobookConfig{Engine: id}.SelectedEngine()
	if err == nil {
		t.Fatalf("SelectedEngine(%q) accepted an engine outside the catalog", id)
	}
	return err
}

// TestAudiobookErrorMapsEveryPreflightRefusal pins all three exits
// Preflight has before the format gate, on one table, because they are
// one decision: each is a way narration is unavailable on this instance,
// so each answers 503 with the code the UI's affordance module branches
// on. Any of them falling through to the default arm is the #221 bug,
// and the third one did until #274.
//
// One code for three causes is deliberate. The obstacle and the fix are
// identical — an admin, in the narration panel — and only the sentence
// differs, which the server states and the client composes (#271). A
// second code would buy a branch that decided the same thing twice.
func TestAudiobookErrorMapsEveryPreflightRefusal(t *testing.T) {
	cases := []struct {
		name string
		err  error
		// wantMessage is exact where the mapper must answer with a
		// particular sentence rather than whatever it was handed.
		wantMessage string
		// mustName is a substring the message has to carry.
		mustName string
	}{{
		// The admin has the feature off. Reached the client as a bare 409
		// until #221, so the UI showed a generic conflict toast instead of
		// the disabled affordance.
		name:        "generation is switched off",
		err:         service.ErrAudiobooksDisabled,
		wantMessage: service.ErrAudiobooksDisabled.Error(),
	}, {
		// Arrives wrapped: Preflight reads settings through the refusing
		// reader NewAudiobookService installs when none is wired, and
		// returns "read audiobook settings: %w". The mapper has to match
		// through the wrapping and answer with the sentinel's own message,
		// because the wrapped one is a log line, not a toast.
		name:        "no settings reader is wired",
		err:         fmt.Errorf("read audiobook settings: %w", service.ErrAudiobooksNotWired),
		wantMessage: service.ErrAudiobooksNotWired.Error(),
	}, {
		// A mistyped engine id, or one an upgrade removed from under a
		// stored selection. The message keeps its wrapping here, because
		// the wrapping is what names the id the admin has to fix.
		name:     "the named engine is not in the catalog",
		err:      unknownEngineError(t, "kokoroo"),
		mustName: "kokoroo",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := audiobookErrorResponse(t, tc.err)

			if status != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503 (body %+v)", status, body)
			}
			if body.Code != CodeAudiobooksDisabled {
				t.Errorf("code = %q, want %q — without it the UI cannot branch",
					body.Code, CodeAudiobooksDisabled)
			}
			if tc.wantMessage != "" && body.Error != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Error, tc.wantMessage)
			}
			if tc.mustName != "" && !strings.Contains(body.Error, tc.mustName) {
				t.Errorf("message = %q, want %q named — it is the one thing telling "+
					"the admin what to fix", body.Error, tc.mustName)
			}
		})
	}
}

// TestAudiobookErrorDefaultsToA409 pins the arm the cases above were
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

// Generate over a run that is still working is the newest member of that
// arm, and it joins deliberately: it is the same category as the two
// above — the caller's view of this run is stale — with the same fix, so
// it gets no code of its own and the client needs no branch for it.
//
// The message is asserted whole because it is the entire answer: with no
// code to switch on, the sentence is what tells a user their run is still
// going and that cancel is the way through (ADR-0031).
func TestAudiobookErrorAnswersARunInProgressWithA409(t *testing.T) {
	status, body := audiobookErrorResponse(t, repo.ErrRunInProgress)

	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %+v)", status, body)
	}
	if body.Code != "" {
		t.Errorf("code = %q, want none", body.Code)
	}
	if body.Error != repo.ErrRunInProgress.Error() {
		t.Errorf("message = %q, want %q", body.Error, repo.ErrRunInProgress.Error())
	}
	if !strings.Contains(body.Error, "cancel") {
		t.Errorf("message = %q, want it to name the way through", body.Error)
	}
}

// ---------------------------------------------------------------------------
// Serving the narration rendition
// ---------------------------------------------------------------------------

// narrationLocationKey is where the generated audio lives, in the
// vocabulary files.location is stored in: relative to the library root.
const narrationLocationKey = "Author/Narrated Book/book.mp3"

// narrationBytes stands in for half a gigabyte of MP3.
var narrationBytes = []byte("id3-and-frames")

// presigningObjectStore is the backend shape that makes the delivery
// decision visible: an object store that can hand out a signed URL, which
// is what an install with EMBOOKSHELF_PRESIGN_FALLBACK=presign asks the
// file-serve path to use rather than piping the bytes through the app.
type presigningObjectStore struct {
	*objectStore
	presigned []string
}

func (s *presigningObjectStore) Capabilities() storage.Capability {
	return storage.CapObjectStore | storage.CapPresign
}

func (s *presigningObjectStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	s.presigned = append(s.presigned, key)
	return "https://signed.example/" + key, nil
}

// narrationFixture is a handler wired to one library holding one book
// with a finished narration, over a real LibraryStore and a real run
// repo — real enough that the storage key rule, the files-row lookup and
// the reconciling read are the ones production runs.
type narrationFixture struct {
	h    *Handler
	book model.Book
}

// newNarrationFixture builds that library over the given Storage. libRoot
// is the library's local path — empty for an object-store library, which
// by design has none. presign selects the delivery mode the deployment
// configured, exactly as app.go passes it through.
func newNarrationFixture(t *testing.T, store storage.Storage, libRoot, presign string) narrationFixture {
	t.Helper()
	return newNarrationFixtureIn(t, store, libRoot, presign, serveReady)
}

// newNarrationFixtureIn is that fixture with the run left in a chosen
// state, so the shared serve suite can drive the narration through the
// same gates as the other two artifacts.
func newNarrationFixtureIn(
	t *testing.T, store storage.Storage, libRoot, presign string, state renditionServeState,
) narrationFixture {
	t.Helper()
	ctx := context.Background()
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)
	runs := repo.NewBookAudiobookRepo(d)

	lib, err := libRepo.CreateLibrary(ctx, "Narrated", "narrated", libRoot, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Narrated Book",
		Author:    "Author",
		Format:    "EPUB",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	// The narration is an ordinary files row in the book's own folder
	// (ADR-0025); what makes it the narration is the run pointing at it.
	audio, err := fileRepo.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		BookID:      book.ID,
		Location:    narrationLocationKey,
		Format:      "MP3",
		Size:        int64(len(narrationBytes)),
		Mtime:       time.Now(),
		LastScanned: time.Now(),
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}
	if state != serveNoRow {
		if _, err := runs.Start(ctx, model.Audiobook{
			BookID: book.ID, Engine: "kokoro", Voice: "af_heart", TotalChars: 10,
		}, nil); err != nil {
			t.Fatalf("Start run: %v", err)
		}
	}
	if state == serveNoBytes || state == serveReady {
		// serveNoBytes is the run finalize never finished writing: ready
		// on the row, pointing at no file.
		fileID := &audio.ID
		if state == serveNoBytes {
			fileID = nil
		}
		if _, err := runs.Transition(ctx, book.ID, model.Transition{
			To:     model.AudiobookReady,
			From:   []model.AudiobookState{model.AudiobookPending},
			FileID: fileID,
		}); err != nil {
			t.Fatalf("Transition to ready: %v", err)
		}
	}

	h := &Handler{
		books:      bookRepo,
		lib:        service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{}, nil),
		audiobooks: service.NewAudiobookService(service.AudiobookDeps{Store: runs}),
		libStore: service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:            libRepo,
			Resolver:        storage.ConstantResolver{S: store},
			Files:           fileRepo,
			PresignTTL:      10 * time.Minute,
			PresignFallback: presign,
		}),
	}
	return narrationFixture{h: h, book: book}
}

// narrationRequest drives the rendition serve with a resolved scope, the
// way the file route does after bookScoped.
func narrationRequest(t *testing.T, f narrationFixture, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/v1/books/"+f.book.ID+"/file?rendition=audio"+query, nil)
	f.h.serveNarrationRendition(c, f.book)
	return rec
}

// The narration is delivered by the same decision as the primary file.
// It used to re-derive its own: whatever the deployment asked for, the
// audio was always piped through the app server, so an install that
// configured presign redirected its EPUBs and streamed its half-gigabyte
// MP3s. One rendition selector cannot mean two delivery policies.
func TestNarrationHonoursThePresignDeliveryDecision(t *testing.T) {
	store := &presigningObjectStore{objectStore: &objectStore{objects: map[string][]byte{
		narrationLocationKey: narrationBytes,
	}}}
	f := newNarrationFixture(t, store, "", service.BookDeliveryPresign)

	rec := narrationRequest(t, f, "")

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "https://signed.example/"+narrationLocationKey {
		t.Errorf("Location = %q, want the signed URL for the narration key", got)
	}
}

// Without presign configured the same decision streams the bytes, and it
// asks the object store for the key the files row names — an object store
// owns its own per-library prefix, so the location is already the key.
func TestNarrationStreamsFromAnObjectStoreBackedLibrary(t *testing.T) {
	store := &objectStore{objects: map[string][]byte{narrationLocationKey: narrationBytes}}
	f := newNarrationFixture(t, store, "", "")

	rec := narrationRequest(t, f, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(narrationBytes) {
		t.Errorf("body = %q, want the narration bytes", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", got)
	}
}

// The local library, whose bytes live under the library root on a
// backend rooted at "/" (ADR-0030 §1). Same decision, same answer.
func TestNarrationServesALocalLibrary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(narrationLocationKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, narrationBytes, 0o600); err != nil {
		t.Fatalf("write mp3: %v", err)
	}
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	f := newNarrationFixture(t, fs, root, "")

	rec := narrationRequest(t, f, "&download=1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(narrationBytes) {
		t.Errorf("body = %q, want the narration bytes", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "Narrated Book.mp3") {
		t.Errorf("Content-Disposition = %q, want the download filename", got)
	}
}
