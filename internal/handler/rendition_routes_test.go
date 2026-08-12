// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// renditionRouteCase adapts one artifact's generate route to the shared
// gate suite: how to build a Handler around the fakes, how to invoke
// the route, and what a success must have enqueued.
type renditionRouteCase struct {
	// build returns a Handler whose artifact store is wired (or nil when
	// withStore is false) plus probes for the started row and the queue.
	build   func(withStore bool, settings *fakeAppSettings, q *captureQueue) (*Handler, func() bool)
	invoke  func(h *Handler, c *gin.Context, s bookScope)
	jobType func(a jobs.Args) bool
}

// TestRenditionGenerateGateChain — the one gate-order suite for both
// generate buttons (#303): nil store → Convertible → requireConverter →
// requireQueue → Start+Enqueue+202. Ran per artifact, so the two routes
// cannot drift the way the deleted
// TestBookEpubGenerateGatesMatchTheMarkdownButton confessed they could.
func TestRenditionGenerateGateChain(t *testing.T) {
	configured := func() *fakeAppSettings {
		return &fakeAppSettings{converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}}
	}

	artifacts := map[string]renditionRouteCase{
		"markdown": {
			build: func(withStore bool, settings *fakeAppSettings, q *captureQueue) (*Handler, func() bool) {
				store := &fakeRenditions{missing: true}
				h := &Handler{appSettings: settings, queue: q}
				if withStore {
					h.renditions = store
					h.mdRequests = service.NewMarkdownRequests(store, q)
				}
				return h, func() bool { return store.started }
			},
			invoke:  func(h *Handler, c *gin.Context, s bookScope) { h.BookMarkdownGenerate(c, s) },
			jobType: func(a jobs.Args) bool { _, ok := a.(jobs.MarkdownRenditionArgs); return ok },
		},
		"epub": {
			build: func(withStore bool, settings *fakeAppSettings, q *captureQueue) (*Handler, func() bool) {
				store := &fakeEpubRenditions{missing: true}
				h := &Handler{appSettings: settings, queue: q}
				if withStore {
					h.epubRenditions = store
					h.epubRequests = service.NewEpubRequests(store, q)
				}
				return h, func() bool { return store.started }
			},
			invoke:  func(h *Handler, c *gin.Context, s bookScope) { h.BookEpubGenerate(c, s) },
			jobType: func(a jobs.Args) bool { _, ok := a.(jobs.EpubRenderArgs); return ok },
		},
	}

	for name, art := range artifacts {
		t.Run(name, func(t *testing.T) {
			t.Run("nil store answers 503", func(t *testing.T) {
				q := &captureQueue{}
				h, _ := art.build(false, configured(), q)
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
			})

			t.Run("non-convertible refused before any gate spends work", func(t *testing.T) {
				q := &captureQueue{}
				h, started := art.build(true, configured(), q)
				s := pdfScope()
				s.Book.Format = "EPUB"
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, s)
				if httpStatus(c, rec) != http.StatusUnsupportedMediaType {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if started() || len(q.enqueued) != 0 {
					t.Fatal("a non-convertible book reached the row or the queue")
				}
			})

			t.Run("not configured refused verbatim", func(t *testing.T) {
				q := &captureQueue{}
				h, started := art.build(true, &fakeAppSettings{}, q)
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "converter extension is not configured") {
					t.Fatalf("body = %s", rec.Body.String())
				}
				if started() || len(q.enqueued) != 0 {
					t.Fatal("an unconfigured converter reached the row or the queue")
				}
			})

			t.Run("no queue answers 503 after the converter gate", func(t *testing.T) {
				h, started := art.build(true, configured(), nil)
				h.queue = nil
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if started() {
					t.Fatal("the row went pending with no queue to work it")
				}
			})

			t.Run("happy path starts the row then enqueues", func(t *testing.T) {
				q := &captureQueue{}
				h, started := art.build(true, configured(), q)
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusAccepted {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if !started() || len(q.enqueued) != 1 || !art.jobType(q.enqueued[0]) {
					t.Fatalf("started = %v, enqueued = %+v", started(), q.enqueued)
				}
			})
		})
	}
}

// --- the serve leg (#316) -------------------------------------------------

// renditionServeState names the states the shared serve suite drives
// every artifact through: the seam missing, nothing generated, a run
// that has not finished, a ready row whose artifact pointer leads
// nowhere, and the one state a download is offered for.
type renditionServeState string

const (
	serveNoRow    renditionServeState = "no row"
	serveNotReady renditionServeState = "not ready"
	serveNoBytes  renditionServeState = "ready without bytes"
	serveReady    renditionServeState = "ready"
)

// renditionServeCase adapts one artifact's serve route to the shared
// suite: a Handler for every state the chain must refuse, one whose
// artifact is ready, and how the route is invoked.
type renditionServeCase struct {
	// unwired is the artifact with its seam missing, plus the answer
	// that must come back: a 503 for markdown's own download route, a
	// 404 for a ?rendition= arm of the file route, which must not claim
	// a book has an artifact this install cannot serve.
	unwired       func(t *testing.T) (*Handler, model.Book)
	unwiredStatus int
	unwiredMsg    string
	// refused are the row states no download is offered for. Every one
	// answers 404 with the artifact's one sentence.
	refused map[renditionServeState]func(t *testing.T) (*Handler, model.Book)
	noneMsg string
	// served is the ready artifact with its bytes in the library.
	served func(t *testing.T) (*Handler, model.Book)
	// query asks that route for an attachment — empty for markdown,
	// whose route is a download route and always answers with one.
	query    string
	mime     string
	filename string
	body     string
	invoke   func(h *Handler, c *gin.Context, book model.Book)
}

// TestRenditionServeGateChain — the one gate-order suite for all three
// serve legs (#316): unwired seam → no row → not ready → a pointer the
// library cannot resolve → the bytes, with the artifact's own sentence
// on every refusal and its own download name on the answer. Ran per
// artifact, so the narration, the generated EPUB and the markdown
// rendition cannot drift apart the way three copied chains could.
func TestRenditionServeGateChain(t *testing.T) {
	artifacts := map[string]renditionServeCase{
		"markdown":  markdownServeCase(),
		"epub":      epubServeCase(),
		"narration": narrationServeCase(),
	}

	for name, art := range artifacts {
		t.Run(name, func(t *testing.T) {
			t.Run("an unwired seam refuses before any lookup", func(t *testing.T) {
				h, book := art.unwired(t)
				c, rec := renditionServeCtx(t, art.query)
				art.invoke(h, c, book)

				if httpStatus(c, rec) != art.unwiredStatus {
					t.Fatalf("status = %d, want %d (body %s)", rec.Code, art.unwiredStatus, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), art.unwiredMsg) {
					t.Fatalf("body = %s, want %q", rec.Body.String(), art.unwiredMsg)
				}
			})

			for state, build := range art.refused {
				t.Run(string(state)+" is not found", func(t *testing.T) {
					h, book := build(t)
					c, rec := renditionServeCtx(t, art.query)
					art.invoke(h, c, book)

					if httpStatus(c, rec) != http.StatusNotFound {
						t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
					}
					if !strings.Contains(rec.Body.String(), art.noneMsg) {
						t.Fatalf("body = %s, want %q", rec.Body.String(), art.noneMsg)
					}
				})
			}

			t.Run("ready serves the bytes under its own download name", func(t *testing.T) {
				h, book := art.served(t)
				c, rec := renditionServeCtx(t, art.query)
				art.invoke(h, c, book)

				if httpStatus(c, rec) != http.StatusOK {
					t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
				}
				if got := rec.Body.String(); got != art.body {
					t.Fatalf("body = %q, want %q", got, art.body)
				}
				if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, art.mime) {
					t.Fatalf("Content-Type = %q, want %q", got, art.mime)
				}
				if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, art.filename) {
					t.Fatalf("Content-Disposition = %q, want %q", got, art.filename)
				}
			})
		})
	}
}

// renditionServeCtx drives a serve route the way the file route does
// after bookScoped: a resolved scope and whatever the caller asked for.
func renditionServeCtx(t *testing.T, query string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	return settingsCtx(t, http.MethodGet, "/api/v1/books/b1/file"+query, "")
}

// markdownServeCase wires the markdown download route. Its bytes go
// through a real local backend — PlaceDerived writes them, StorageKey
// finds them again.
func markdownServeCase() renditionServeCase {
	row := func(r model.MarkdownRendition) *fakeRenditions { return &fakeRenditions{row: r} }
	withLibStore := func(store markdownRenditionStore) (*Handler, model.Book) {
		return &Handler{
			renditions: store,
			libStore:   fakeLibStore{handle: &service.LibraryHandle{}},
		}, model.Book{ID: "b1", Title: "Dune"}
	}
	return renditionServeCase{
		unwired: func(*testing.T) (*Handler, model.Book) {
			return &Handler{libStore: fakeLibStore{handle: &service.LibraryHandle{}}},
				model.Book{ID: "b1", Title: "Dune"}
		},
		unwiredStatus: http.StatusServiceUnavailable,
		unwiredMsg:    "markdown renditions are unavailable",
		noneMsg:       "this book has no markdown rendition",
		refused: map[renditionServeState]func(t *testing.T) (*Handler, model.Book){
			serveNoRow: func(*testing.T) (*Handler, model.Book) {
				return withLibStore(&fakeRenditions{missing: true})
			},
			serveNotReady: func(*testing.T) (*Handler, model.Book) {
				return withLibStore(row(model.MarkdownRendition{State: model.RenditionRunning}))
			},
			serveNoBytes: func(*testing.T) (*Handler, model.Book) {
				return withLibStore(row(model.MarkdownRendition{State: model.RenditionReady}))
			},
		},
		served: markdownServed,
		// No ?download flag: the markdown route is a download route, and
		// its answer is always an attachment.
		query:    "",
		mime:     "text/markdown",
		filename: `filename="Dune.md"`,
		body:     "# markdown body\n",
		invoke: func(h *Handler, c *gin.Context, book model.Book) {
			h.BookMarkdownDownload(c, bookScope{UserID: "u1", Book: book})
		},
	}
}

func markdownServed(t *testing.T) (*Handler, model.Book) {
	t.Helper()
	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}
	book := model.Book{ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "A", Format: "PDF"}

	tmp := filepath.Join(t.TempDir(), "staged.md")
	if err := os.WriteFile(tmp, []byte("# markdown body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	placed, err := handle.PlaceDerived(context.Background(), book, tmp, service.DerivedMarkdown)
	if err != nil {
		t.Fatalf("PlaceDerived: %v", err)
	}
	return &Handler{
		renditions: &fakeRenditions{row: model.MarkdownRendition{
			BookID: book.ID, State: model.RenditionReady, Location: placed.Location,
		}},
		libStore: fakeLibStore{handle: handle},
	}, book
}

// epubRenditionKey is where a generated EPUB lives, in the vocabulary
// files.location is stored in: relative to the library root.
const epubRenditionKey = "Author/Rendered Book/book.epub"

var epubRenditionBytes = []byte("PK-generated-epub")

// epubServeCase wires the ?rendition=epub arm of the file route.
func epubServeCase() renditionServeCase {
	danglingFileID := "no-such-file"
	// A handle with no files behind it: the row points at a files row
	// the library cannot resolve, which is the locate step's own 404.
	blindStore := fakeLibStore{handle: &service.LibraryHandle{}}
	withLibStore := func(store epubRenditionStore) (*Handler, model.Book) {
		return &Handler{epubRenditions: store, libStore: blindStore},
			model.Book{ID: "b1", Title: "Rendered Book"}
	}
	return renditionServeCase{
		unwired: func(*testing.T) (*Handler, model.Book) {
			return &Handler{libStore: blindStore}, model.Book{ID: "b1", Title: "Rendered Book"}
		},
		unwiredStatus: http.StatusNotFound,
		unwiredMsg:    "this book has no generated EPUB",
		noneMsg:       "this book has no generated EPUB",
		refused: map[renditionServeState]func(t *testing.T) (*Handler, model.Book){
			serveNoRow: func(*testing.T) (*Handler, model.Book) {
				return withLibStore(&fakeEpubRenditions{missing: true})
			},
			serveNotReady: func(*testing.T) (*Handler, model.Book) {
				return withLibStore(&fakeEpubRenditions{row: model.EpubRendition{
					State: model.RenditionRunning,
				}})
			},
			serveNoBytes: func(*testing.T) (*Handler, model.Book) {
				return withLibStore(&fakeEpubRenditions{row: model.EpubRendition{
					State: model.RenditionReady,
				}})
			},
			"pointing at a file the library lost": func(*testing.T) (*Handler, model.Book) {
				return withLibStore(&fakeEpubRenditions{row: model.EpubRendition{
					State: model.RenditionReady, FileID: &danglingFileID,
				}})
			},
		},
		served:   epubServed,
		query:    "?rendition=epub&download=1",
		mime:     model.MIMEForFormat("EPUB"),
		filename: `filename="Rendered Book.epub"`,
		body:     string(epubRenditionBytes),
		invoke: func(h *Handler, c *gin.Context, book model.Book) {
			h.serveEpubRendition(c, book)
		},
	}
}

// epubServed builds a library holding one book whose generated EPUB is
// a real files row over a real LibraryStore — the files-row lookup and
// the storage key rule are the ones production runs. The tracking row
// stays a fake: what it points at is the subject here, not how it is
// stored.
func epubServed(t *testing.T) (*Handler, model.Book) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(epubRenditionKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, epubRenditionBytes, 0o600); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	d := repotest.New(t)
	libRepo := repo.NewLibraryRepo(d)
	bookRepo := repo.NewBookRepo(d)
	fileRepo := repo.NewFileRepo(d)

	lib, err := libRepo.CreateLibrary(ctx, "Rendered", "rendered", root, nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	book, err := bookRepo.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "Rendered Book",
		Author:    "Author",
		Format:    "PDF",
	})
	if err != nil {
		t.Fatalf("Create book: %v", err)
	}
	// The generated EPUB is an ordinary files row beside the book's own
	// file; what makes it the rendition is the tracking row pointing at
	// it (ADR-0034).
	rendered, err := fileRepo.Insert(ctx, model.File{
		LibraryID:   lib.ID,
		BookID:      book.ID,
		Location:    epubRenditionKey,
		Format:      "EPUB",
		Size:        int64(len(epubRenditionBytes)),
		Mtime:       time.Now(),
		LastScanned: time.Now(),
	})
	if err != nil {
		t.Fatalf("Insert file: %v", err)
	}

	return &Handler{
		epubRenditions: &fakeEpubRenditions{row: model.EpubRendition{
			BookID: book.ID, State: model.RenditionReady, FileID: &rendered.ID,
		}},
		libStore: service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:     libRepo,
			Resolver: storage.ConstantResolver{S: rootedAtSlash},
			Files:    fileRepo,
		}),
	}, book
}

// narrationServeCase wires the ?rendition=audio arm — the third adapter
// of the seam, and the one ADR-0025 is about.
func narrationServeCase() renditionServeCase {
	narration := func(state renditionServeState) func(t *testing.T) (*Handler, model.Book) {
		return func(t *testing.T) (*Handler, model.Book) {
			t.Helper()
			f := narrationServeFixture(t, state)
			return f.h, f.book
		}
	}
	return renditionServeCase{
		unwired: func(*testing.T) (*Handler, model.Book) {
			// No library store: nothing to resolve the audio through.
			return &Handler{}, model.Book{ID: "b1", Title: "Narrated Book"}
		},
		unwiredStatus: http.StatusNotFound,
		unwiredMsg:    "this book has no generated narration",
		noneMsg:       "this book has no generated narration",
		refused: map[renditionServeState]func(t *testing.T) (*Handler, model.Book){
			serveNoRow:    narration(serveNoRow),
			serveNotReady: narration(serveNotReady),
			serveNoBytes:  narration(serveNoBytes),
			"pointing at a file the library lost": func(t *testing.T) (*Handler, model.Book) {
				f := narrationServeFixture(t, serveReady)
				// A ready run over a handle with no files behind it.
				f.h.libStore = fakeLibStore{handle: &service.LibraryHandle{}}
				return f.h, f.book
			},
		},
		served:   narration(serveReady),
		query:    "?rendition=audio&download=1",
		mime:     "audio/mpeg",
		filename: `filename="Narrated Book.mp3"`,
		body:     string(narrationBytes),
		invoke: func(h *Handler, c *gin.Context, book model.Book) {
			h.serveNarrationRendition(c, book)
		},
	}
}

// narrationServeFixture is the local-library narration fixture in a
// chosen state — the same wiring TestNarrationServesALocalLibrary uses.
func narrationServeFixture(t *testing.T, state renditionServeState) narrationFixture {
	t.Helper()
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
	return newNarrationFixtureIn(t, fs, root, "", state)
}

// TestBookFileWritesTheOneContentDispositionSentence — the book file
// route is the fourth download surface, and the header it writes is the
// same sentence the three renditions write: RFC 6266's bare filename=
// carrying the ASCII degrade for old clients, plus the extended
// UTF-8 form carrying the real name. Pinned whole, because the halves
// are only useful together.
func TestBookFileWritesTheOneContentDispositionSentence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "book.epub")
	if err := os.WriteFile(path, []byte("epub-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: config.Config{BookDropPath: root}}
	book := model.Book{ID: "b1", Title: "Sœur", Author: "Élan", Format: "EPUB", Path: path}
	scope := bookScope{UserID: "u1", Book: book}

	t.Run("inline until a download is asked for", func(t *testing.T) {
		c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/file", "")
		h.BookFile(c, scope)

		if httpStatus(c, rec) != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Disposition"); got != "" {
			t.Fatalf("Content-Disposition = %q, want none for the in-browser reader", got)
		}
	})

	t.Run("?download names the file both ways", func(t *testing.T) {
		c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/file?download=1", "")
		h.BookFile(c, scope)

		if httpStatus(c, rec) != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		want := `attachment; filename="_lan - S_ur.epub"; filename*=UTF-8''%C3%89lan%20-%20S%C5%93ur.epub`
		if got := rec.Header().Get("Content-Disposition"); got != want {
			t.Fatalf("Content-Disposition = %q, want %q", got, want)
		}
	})
}
