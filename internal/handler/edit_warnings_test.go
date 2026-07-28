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

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// A degraded write is the case ADR-0001 is built to survive: the books row
// landed, so the edit is not lost, but the Sidecar or the in-file copy did
// not keep up and the file on disk no longer carries the edit. Only the
// user can act on that, and only if we tell them. The metadata PATCH and
// the lock toggle already put Outcome.Warnings() on the response; apply
// match discarded the Outcome at the service and returned an unqualified
// 200 — the one edit endpoint of three where a silent degradation was
// invisible. These tests drive all three through the same broken Sidecar
// and demand the same answer from each.

const warnTestUserID = "aaaaaaaa-0001-4001-8001-0000000000ff"

// brokenSidecar fails the Sidecar step of the pipeline. The in-file step
// is left out of the writer entirely (nil Dispatch), so exactly one step
// degrades and the warning list has one predictable entry.
type brokenSidecar struct{}

func (brokenSidecar) Write(
	context.Context, storage.Storage, string, sidecar.Sidecar, sidecar.WriteMode, string,
) error {
	return errors.New("sidecar disk full")
}

// fixedLibStore hands the writer one library handle regardless of id.
type fixedLibStore struct{ handle *service.LibraryHandle }

func (f fixedLibStore) For(context.Context, string) (*service.LibraryHandle, error) {
	return f.handle, nil
}

// degradedEditHandler wires the three edit endpoints over a real database
// and a MetadataWriter whose Sidecar step always fails. Returns the
// handler and the id of a book to edit.
func degradedEditHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	ctx := context.Background()
	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	shelfRepo := repo.NewShelfRepo(d)

	// Library Path stays empty so the folder-rename step declines rather
	// than adding a second, unrelated warning.
	lib, err := libRepo.CreateLibrary(ctx, "Fiction", "fiction", "", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Stored Title",
		Author:    "Stored Author",
		Format:    "CBZ",
		Path:      "books/x.cbz",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	writer := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    bookRepo,
		LibStore: fixedLibStore{&service.LibraryHandle{Library: lib, Storage: fs}},
		Sidecar:  brokenSidecar{},
	})

	h := &Handler{
		lib: service.NewLibraryService(
			libRepo, bookRepo, service.LibraryServiceDeps{}, writer),
		// Book reads go straight to the repo now that LibraryService is
		// the Library lifecycle module and no longer fronts the catalog.
		books: bookRepo,
		// The enrichment service reaches for providers and covers only on
		// paths this test does not take; the writer is the dependency
		// under test.
		enrich: service.NewEnrichmentService(nil, nil, nil, nil, writer),
		shelf:  service.NewShelfService(shelfRepo, nil),
	}
	return h, book.ID
}

// editRequest drives one handler func with a signed-in user and a book id.
func editRequest(t *testing.T, fn func(*gin.Context), method, target, bookID, body string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	c.Request = r.WithContext(
		auth.WithUser(r.Context(), &model.User{ID: warnTestUserID}))
	c.Params = gin.Params{{Key: "id", Value: bookID}}

	fn(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a degraded write still saved the edit: %s",
			rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return got
}

// assertWarnings demands the shared shape: a top-level "warnings" array of
// strings that names the step that did not happen and carries its cause.
func assertWarnings(t *testing.T, body map[string]any, endpoint string) {
	t.Helper()
	raw, ok := body["warnings"]
	if !ok {
		t.Fatalf("%s: no warnings on a degraded write — the response says the edit fully landed", endpoint)
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("%s: warnings = %#v, want a non-empty array of strings", endpoint, raw)
	}
	first, ok := list[0].(string)
	if !ok {
		t.Fatalf("%s: warnings[0] = %#v, want a string", endpoint, list[0])
	}
	if !strings.Contains(first, "sidecar") {
		t.Errorf("%s: warning %q does not name the failed step", endpoint, first)
	}
	if !strings.Contains(first, "disk full") {
		t.Errorf("%s: warning %q does not carry the cause", endpoint, first)
	}
	if _, ok := body["book"]; !ok {
		t.Errorf("%s: warnings replaced the book instead of accompanying it", endpoint)
	}
}

func TestMetadataPatchReportsDegradedWrite(t *testing.T) {
	h, bookID := degradedEditHandler(t)
	body := editRequest(t, h.bookScoped(h.BookPatch), http.MethodPatch,
		"/api/v1/books/"+bookID, bookID, `{"title":"Edited Title"}`)
	assertWarnings(t, body, "PATCH /books/:id")
}

func TestFieldLockToggleReportsDegradedWrite(t *testing.T) {
	h, bookID := degradedEditHandler(t)
	body := editRequest(t, h.bookScoped(h.EnrichToggleFieldLocks), http.MethodPut,
		"/api/v1/books/"+bookID+"/metadata/locks", bookID, `{"locks":{"title":true}}`)
	assertWarnings(t, body, "PUT /books/:id/metadata/locks")
}

// The regression. Apply match ran the same pipeline as the other two and
// then threw the Outcome away, so a provider match applied to a book whose
// Sidecar (or rezip) failed came back as a clean 200.
func TestApplyMatchReportsDegradedWrite(t *testing.T) {
	h, bookID := degradedEditHandler(t)
	body := editRequest(t, h.bookScoped(h.EnrichApplyMatch), http.MethodPut,
		"/api/v1/books/"+bookID+"/metadata", bookID,
		`{"source":"googlebooks","sourceId":"g1","title":"Provider Title"}`)
	assertWarnings(t, body, "PUT /books/:id/metadata")
}
