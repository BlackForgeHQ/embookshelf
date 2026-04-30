# Streaming + Presigned URLs — Implementation Plan (Plan G of 8)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Use the `Storage` capabilities Plan F enabled (`CapPresign`, `CapRange`) at the wire. The book-file handler now redirects to a presigned URL when the file lives on a backend that supports it (S3); local-FS backends continue to serve via `c.File()` which already honors `Range:` headers natively.

**Architecture:** A new `BookSource` resolver in `internal/handler` (or as a method on the library service) takes a `book` and returns one of two outcomes: `{Kind: "local", Path: "/abs/path"}` (existing `c.File()` path) or `{Kind: "presign", URL: "https://...", TTL: 10m}` (302 redirect). The resolver consults the book → library → backend_id chain via the existing repo + resolver. Presign TTL is 10 minutes by default; configurable via `EMBOOKSHELF_PRESIGN_TTL`.

**Tech Stack:** Reuses Plan F's `storage.Resolver` and `s3.Backend.PresignGet`. No new third-party deps.

**Companion spec:** [`docs/spec/storage.spec.md`](../../spec/storage.spec.md) §8 (streaming and access).

**Locked decisions:**
- Default presign TTL: 10 minutes. Tunable via env (`EMBOOKSHELF_PRESIGN_TTL`).
- Local backend: keep `c.File()` — it already does HTTP 206 Partial Content.
- 302 redirect (not 307) — clients re-resolve the URL on each request, which is what we want as the URL is short-lived.
- Cover handler stays local-only (covers always live in the local cache per spec §7) — no presign there.
- `EMBOOKSHELF_PRESIGN_FALLBACK=stream` env-flag (default off) lets ops disable redirects and stream through the app server instead. Useful for clients that can't follow cross-origin redirects.

**Depends on:** Plan F merged (`storage.Resolver`, `s3.Backend.PresignGet`, capability bits).

**Out of scope:**
- Frontend changes (the audiobook reader already requests via standard fetch; the redirect is transparent to it).
- Sidecar serving via presign — sidecars are tiny and only the app reads them.
- S3 events / lifecycle — Plan H.

---

## File Structure

### Files created

| Path | Responsibility |
|---|---|
| `internal/handler/booksource.go` | `BookSource` resolver: presign vs local. |
| `internal/handler/booksource_test.go` | Unit tests with stub resolver + library service. |

### Files modified

| Path | Change |
|---|---|
| `internal/handler/files.go` | `serveBookFile` consults the BookSource resolver before falling back to `c.File()`. |
| `internal/handler/handler.go` (or wherever `Handler` is defined) | Add `Resolver storage.Resolver`, `LibraryRepo *repo.LibraryRepo`, `FileRepo *repo.FileRepo`, `PresignTTL time.Duration` fields to `Handler`. |
| `cmd/embookshelf/main.go` | Pass the new fields when constructing the handler. Read `EMBOOKSHELF_PRESIGN_TTL`. |
| `internal/config/config.go` | Add `PresignTTL time.Duration` (default 10 minutes), `PresignFallback string` to the env-config loader. |

---

## Phase 1 — BookSource Resolver

### Task 1: `internal/handler/booksource.go`

```go
package handler

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/blackforge/embookshelf/internal/model"
    "github.com/blackforge/embookshelf/internal/repo"
    "github.com/blackforge/embookshelf/internal/storage"
    s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
)

// BookSource describes how the handler should serve a book file.
type BookSource struct {
    Kind    string // "local" or "presign"
    Path    string // populated when Kind == "local"
    URL     string // populated when Kind == "presign"
    TTL     time.Duration
}

// Presigner is the capability-gated interface that any backend with
// CapPresign must satisfy. Defined here (not in storage) so the
// handler can probe via type assertion without leaking aws-sdk types.
type Presigner interface {
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ResolveBookSource determines whether to redirect or serve locally.
// Lookup chain: book -> files row(s) -> library.backend_id ->
// Resolver.Resolve -> Capabilities() -> presign or local.
func ResolveBookSource(
    ctx context.Context,
    book model.Book,
    files *repo.FileRepo,
    libs *repo.LibraryRepo,
    resolver storage.Resolver,
    presignTTL time.Duration,
    fallback string, // "" or "stream"
) (BookSource, error) {
    if book.Path == "" {
        return BookSource{}, errors.New("book has no path")
    }
    // Default outcome: local, using book.Path. Plan B kept book.Path
    // populated alongside files.location, so single-backend installs
    // continue to work without files-table backfill.
    src := BookSource{Kind: "local", Path: book.Path}

    if resolver == nil || files == nil || libs == nil {
        return src, nil
    }

    // Resolve the library's backend.
    lib, err := libs.GetByID(ctx, book.LibraryID)
    if err != nil {
        return src, nil // can't resolve → fall back to local
    }
    backendID := ""
    if lib.BackendID != nil {
        backendID = *lib.BackendID
    }
    backend, err := resolver.Resolve(backendID)
    if err != nil {
        return src, nil
    }

    // If the backend can presign and we're not forcing stream,
    // look up the file's location and presign it.
    if backend.Capabilities()&storage.CapPresign != 0 && fallback != "stream" {
        ps, ok := backend.(Presigner)
        if !ok {
            return src, nil // unexpected; fall back to local
        }
        // Find the canonical files row for this book. Pick the
        // "primary" file by matching books.format → files.format.
        // If no row exists yet (pre-files-backfill), fall back.
        f, err := primaryFile(ctx, files, book)
        if err != nil {
            return src, nil
        }
        url, err := ps.PresignGet(ctx, f.Location, presignTTL)
        if err != nil {
            return src, nil
        }
        return BookSource{Kind: "presign", URL: url, TTL: presignTTL}, nil
    }
    return src, nil
}

func primaryFile(ctx context.Context, files *repo.FileRepo, book model.Book) (model.File, error) {
    // Plan B's FileRepo doesn't have a "by book id" lookup yet —
    // add it as part of this plan if missing. ListByBook returns all
    // files for a book; we pick the one matching books.format.
    list, err := files.ListByBook(ctx, book.ID)
    if err != nil {
        return model.File{}, err
    }
    for _, f := range list {
        if f.Format == book.Format {
            return f, nil
        }
    }
    if len(list) > 0 {
        return list[0], nil
    }
    return model.File{}, fmt.Errorf("no files row for book %s", book.ID)
}

// Compile-time assertion that S3 Backend satisfies Presigner.
var _ Presigner = (*s3backend.Backend)(nil)
```

Add `FileRepo.ListByBook(ctx, bookID) ([]model.File, error)` if it doesn't exist already (search `internal/repo/file.go` for it; if absent, add it — straightforward `SELECT … FROM files WHERE book_id = $1`).

Tests in `booksource_test.go`:
- Nil resolver → `Kind: "local"`.
- LocalFS backend (capabilities=0) → `Kind: "local"`.
- Mock Presigner backend with capabilities including `CapPresign` → `Kind: "presign"` with URL.
- `fallback == "stream"` with presign-capable backend → `Kind: "local"`.
- File row missing → `Kind: "local"` (graceful fallback).

Use a small Presigner stub in the test:

```go
type stubPresigner struct {
    storage.Storage
    cap storage.Capability
    url string
}

func (s *stubPresigner) Capabilities() storage.Capability { return s.cap }
func (s *stubPresigner) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
    return s.url, nil
}
```

Commit: `feat(handler): BookSource resolver with presign fast-path`.

---

## Phase 2 — Files Handler Cutover

### Task 2: Update `serveBookFile`

In `internal/handler/files.go`, change the signature to take a `model.Book` (or pass the relevant fields) and use the resolver:

```go
// serveBookFile chooses between local serve and presigned redirect
// based on the book's backing storage backend.
func (h *Handler) serveBookFile(c *gin.Context, book model.Book, mime string) error {
    src, err := ResolveBookSource(
        c.Request.Context(), book,
        h.files, h.lib, h.resolver,
        h.presignTTL, h.cfg.PresignFallback,
    )
    if err != nil {
        return err
    }
    if src.Kind == "presign" {
        c.Redirect(http.StatusFound, src.URL)
        return nil
    }
    return h.serveLocalBookFile(c, src.Path, mime)
}
```

`serveLocalBookFile` is the existing body of `serveBookFile`, renamed. The `path` argument is the absolute disk path; the rooting/allow-list check stays unchanged.

Update the callers of `serveBookFile`:
- `internal/handler/library.go` `BookFile` handler — already loads the book; pass it directly.
- `internal/handler/comic.go` (if it uses `serveBookFile`) — likewise.
- `internal/handler/opds.go` (if it has a download path) — likewise.

For places that have the `book` model loaded from a previous step, this is a one-line change. For places that only have the path string, load the book via `h.lib.GetBookByID` (or whatever the existing helper is).

Commit: `feat(handler): serveBookFile redirects to presign for S3-backed books`.

### Task 3: Wire deps in `Handler` + `main.go` + config

`internal/handler/handler.go` (or whatever the `Handler` struct file is — find via `grep -n "type Handler struct" internal/handler/`):

```go
type Handler struct {
    // ... existing fields ...
    resolver    storage.Resolver
    files       *repo.FileRepo
    presignTTL  time.Duration
}
```

Constructor takes the new args.

`internal/config/config.go`:

```go
PresignTTL       time.Duration `env:"EMBOOKSHELF_PRESIGN_TTL" envDefault:"10m"`
PresignFallback  string        `env:"EMBOOKSHELF_PRESIGN_FALLBACK"`
```

`cmd/embookshelf/main.go`: pass `storageResolver`, `fileRepo`, and `cfg.PresignTTL` into the handler constructor.

Commit: `feat(config+handler): EMBOOKSHELF_PRESIGN_TTL + handler resolver wiring`.

---

## Phase 3 — Verification

### Task 4: Verify and PR

- [ ] `make ci-local` green.
- [ ] Manual smoke (if S3 setup available): create an S3-backed library; request `/api/books/:id/file`; observe 302 to a presigned URL.
- [ ] Local-only smoke: existing dev DB; book-file requests still serve via `c.File()`.
- [ ] Push, open PR.

---

## Self-Review

**Spec coverage:**
- §8.1 range reads → covered for local (existing `c.File()`); for S3, the presign URL the client follows already supports Range.
- §8.2 presigned URLs → covered.
- §8.3 storage class / lifecycle → Plan H.

**Risks:**
- The 302 redirect crosses origins (S3 endpoint vs app server). Browsers handle it; native clients may need to follow redirects explicitly. The `EMBOOKSHELF_PRESIGN_FALLBACK=stream` env-flag is the escape hatch.
- Presign TTL of 10 min is a guess. Audiobook listening sessions can be longer; the URL becoming invalid mid-stream is annoying. The reader UI re-fetches on demand so it's tolerable; if it bites, bump the env or implement URL refresh on `<audio>` `error` events.
- A book whose `files` row is still missing (pre-backfill) falls back to local serve via `book.path`. This is correct behavior.
- The `EMBOOKSHELF_PRESIGN_FALLBACK=stream` path proxies the bytes through the app server — same memory profile as today's `c.File()`. We don't try to stream chunks via `storage.Get(WithRange)` in that mode (would require Range header parsing). Acceptable.

**Type consistency:** `BookSource`, `ResolveBookSource`, `Presigner`, `primaryFile`, `serveLocalBookFile` consistent across files.
