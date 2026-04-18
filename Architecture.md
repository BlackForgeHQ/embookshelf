# Embookshelf - Architecture Document

## 1. High-Level Architecture

Embookshelf is a monolithic full-stack application built in Go. The server renders HTML on demand with [Templ](https://templ.guide) and drives interactivity via [HTMX 4](https://htmx.org) partial swaps, eliminating the need for a separate SPA. Styling is handled with [Tailwind CSS 4](https://tailwindcss.com). Data lives in PostgreSQL.

```
┌─────────────────────────────────────────────────────┐
│                   Docker Container                   │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │           Go server (port 6060)                │  │
│  │                                               │  │
│  │  ┌──────────┐  ┌──────────┐  ┌────────────┐  │  │
│  │  │ Templ    │  │ HTTP     │  │    SSE     │  │  │
│  │  │ pages +  │  │ handlers │  │  /events   │  │  │
│  │  │ partials │  │ /app/*   │  │ (htmx 4)   │  │  │
│  │  └──────────┘  └────┬─────┘  └─────┬──────┘  │  │
│  │                     │              │          │  │
│  │  ┌──────────────────┴──────────────┴───────┐  │  │
│  │  │           Service Layer                  │  │  │
│  │  └──────────────────┬──────────────────────┘  │  │
│  │                     │                         │  │
│  │  ┌─────────┐  ┌─────┴─────┐  ┌────────────┐  │  │
│  │  │  pgx /  │  │ File I/O  │  │ External   │  │  │
│  │  │  sqlc   │  │ (books)   │  │ APIs       │  │  │
│  │  └────┬────┘  └───────────┘  └────────────┘  │  │
│  └───────┼───────────────────────────────────────┘  │
│          │                                          │
└──────────┼──────────────────────────────────────────┘
           │
    ┌──────┴──────┐
    │  PostgreSQL │
    │    16+      │
    └─────────────┘
```

---

## 2. Technology Stack

| Layer | Technology | Version |
|-------|-----------|---------|
| **Runtime** | Go | 1.23+ |
| **HTTP Router** | Gin ([gin-gonic/gin](https://github.com/gin-gonic/gin)) | 1.x |
| **Templating** | Templ | 0.3+ |
| **Interactivity** | HTMX | 4.x |
| **Styling** | Tailwind CSS | 4.x |
| **Database** | PostgreSQL | 16+ |
| **DB Driver** | pgx (v5) | 5.x |
| **Query Codegen** | sqlc (staged, hand-written pgx today) | 1.x |
| **Migrations** | [golang-migrate/migrate](https://github.com/golang-migrate/migrate) | 4.x |
| **Auth** | golang-jwt + coreos/go-oidc | — |
| **Sessions** | alexedwards/scs + scs/pgxstore | — |
| **Background Jobs** | river (Postgres-backed queue) | 0.x |
| **Cache** | ristretto | 1.x |
| **Real-time** | Server-Sent Events via Gin handler | — |
| **Validation** | go-playground/validator | 10.x |
| **Testing (backend)** | testing + testify + pgtest | — |
| **Testing (e2e)** | Playwright | 1.x |
| **Build (frontend assets)** | Tailwind CLI + `templ generate` | — |
| **Containerization** | Docker (multi-stage) | — |

---

## 3. Project Structure

```
embookshelf/
├── cmd/
│   ├── embookshelf/                # main.go — composition root
│   └── migrate/                    # CLI around internal/migrator (up/down/version/force)
│
├── internal/
│   ├── handler/                    # HTTP handlers (HTMX-aware)
│   │   ├── auth.go                 # login/logout/signup
│   │   ├── book.go                 # detail / edit / update / progress / shelf-toggle
│   │   ├── bookdrop.go             # review queue + SSE /events
│   │   ├── cover.go                # /app/cover/:id, /app/bookdrop/:id/cover
│   │   ├── enrich.go               # /app/book/:id/enrich, cover-from-url
│   │   ├── home.go                 # dashboard
│   │   ├── library.go              # /app/library (search/sort/filter)
│   │   ├── opds.go                 # /opds/* catalog
│   │   ├── reader.go               # /app/read/:id + /app/read/:id/file
│   │   ├── settings.go             # /app/settings, /app/settings/libraries
│   │   ├── health.go               # /api/v1/healthcheck
│   │   ├── handler.go              # Deps struct + helpers
│   │   └── router.go               # gin.Engine assembly + middleware wiring
│   │
│   ├── service/                    # Business logic
│   │   ├── auth.go                 # Login/Logout/Verify/Signup + session TTL
│   │   ├── bookdrop.go             # ingest state machine + SSE broadcast
│   │   ├── enrichment.go           # metadata fan-out + cover-from-URL
│   │   ├── library.go              # book lookup + update
│   │   ├── library_path.go         # filesystem roots per library
│   │   ├── progress.go             # per-user reading progress
│   │   └── shelf.go                # per-user shelves CRUD
│   │
│   ├── repo/                       # pgx-backed repositories (hand-written SQL)
│   │   └── queries/                # *.sql kept ready for a future sqlc pass
│   │
│   ├── model/                      # Domain structs
│   │
│   ├── view/                       # .templ files — pages, partials, components
│   │   ├── layout/                 # shells (app, auth)
│   │   ├── page/                   # full-page views
│   │   ├── partial/                # HTMX swap targets
│   │   └── component/              # reusable (Cover, Sidebar, TopBar, MatchCard, ...)
│   │
│   ├── auth/                       # context, password, session cookie, middleware, basic
│   ├── coverstore/                 # filesystem store for extracted cover images
│   ├── fileproc/                   # Format processors — EPUB today; PDF/CBX/audio planned
│   ├── ingest/                     # BookDrop folder watcher (polling)
│   ├── middleware/                 # htmx-detect helpers
│   ├── migrator/                   # embedded migrations + golang-migrate wrapper
│   │   └── migrations/             # NNN_name.up.sql / .down.sql
│   ├── opds/                       # Atom/XML feed types + builder
│   ├── provider/                   # Google Books + Open Library metadata providers
│   ├── queue/                      # river client wrapper (EnqueueBookDrop, EnqueueLibraryScan)
│   ├── sse/                        # Server-Sent Events fan-out hub
│   ├── staticfs/                   # //go:embed for Tailwind output + htmx/sse/reader JS
│   ├── task/                       # river workers — BookDropWorker, LibraryScanWorker
│   └── config/                     # env loading
│
├── web/
│   └── src/styles.css              # Tailwind 4 entry + design tokens
│
├── scripts/                        # dev tooling (seed.sql)
├── Dockerfile                      # Multi-stage production build
├── compose.dev.yml                 # Development environment (Postgres + migrate one-shot)
├── go.mod                          # Go 1.24; `tool` directives for templ + air
├── go.sum
├── sqlc.yaml                       # staged for future generator pass
├── Makefile                        # dev/build/assets/migrate/seed targets
├── .air.toml                       # live-reload config (`go tool air`)
└── package.json                    # Tailwind CLI (npx @tailwindcss/cli)
```

Deferred scaffolding (not yet created): `compose.example.yml`, `deploy/helm/`,
`.github/workflows/` — spelled out in §9 so the shape is documented before
the folders exist.

---

## 4. Backend Architecture

### 4.1 Layered Design

```
Handler → Service → Repository → PostgreSQL
             ↕
      File Processors
             ↕
      External APIs (Google Books, Open Library, Amazon)
```

- **Handlers** — Gin `HandlerFunc`s mounted under `/app/*` (HTML), `/opds/*` (Atom/XML), and `/api/v1/*` (JSON, currently just healthcheck). HTMX requests are detected via the `HX-Request` header; handlers return full pages on direct navigation and partials on HTMX swaps. Dependencies are passed in via a `Deps` struct to keep `handler.New`'s signature flat as the app grows.
- **Services** — Business logic. Plain Go structs wired with constructor functions in `cmd/embookshelf/main.go`.
- **Repositories** — `pgx/v5` with hand-written SQL. Queries are ready to migrate to `sqlc` once the schema stabilizes; `sqlc.yaml` + `internal/repo/queries/*.sql` are already in place for that pass.
- **Views** — Templ components render responses. A page handler composes a layout + page component; a partial handler returns a fragment targeting a specific HTMX swap.
- **DTOs** — Request/response structs are colocated with handlers.

### 4.2 Concurrency Model

- **Goroutines** handle every request; all I/O (DB, file, HTTP) is naturally non-blocking on the Go scheduler.
- **pgxpool** connection pool: 20 max connections, 5 min idle (tunable via env).
- `context.Context` is threaded through every call and respects request cancellation.
- **Caching** is intentionally absent today. Covers + static assets get HTTP
  `Cache-Control` headers; hot-path in-memory caching (ristretto) is a planned
  addition once a specific hot spot warrants it.

### 4.3 File Processing Pipeline

Each format has a dedicated processor implementing a common interface:

```go
type Metadata struct {
    Title, Author, Description, Language string
    HasCover   bool
    CoverBytes []byte   // optional — populated when the format embeds a cover
    CoverMime  string
    Format     string
}

type Processor interface {
    Extract(ctx context.Context, path string) (Metadata, error)
}
```

Extraction returns metadata + cover bytes in one pass so the archive only
has to be opened once. Cover bytes are handed off to `coverstore` (which
writes atomically under `${DATA_PATH}/covers/`), and the presence/MIME are
stored on `books`/`bookdrop_items`.

| Processor | Status |
|-----------|--------|
| `EPUBProcessor` | **Built** — stdlib `archive/zip` + `encoding/xml`, no external dependency |
| `PDFProcessor` | Deferred — metadata extraction from PDFs via `pdfcpu` when added |
| `CBXProcessor` | Deferred — `archive/zip` + `nwaples/rardecode` for CBR |
| `AudiobookProcessor` | Deferred — `dhowden/tag` |
| `AZW3`, `MOBI`, `FB2` | Deferred |

Non-EPUB files dropped today are queued, surface a permanent-failed state in
the review UI (`ErrUnsupportedFormat`), and can still be approved manually —
they just carry no extracted metadata.

### 4.4 Async Task System

Background work uses **river**, a Postgres-backed job queue:

- `internal/queue/` wraps the `river.Client` so the service layer stays free
  of river imports. River's own DB schema is applied at boot via
  `rivermigrate` — no manual migration step.
- `internal/task/` contains per-job-kind workers:
  - `BookDropWorker` (`bookdrop.ingest`) — runs the `fileproc` pipeline,
    stores the cover, transitions the queue row.
  - `LibraryScanWorker` (`library.scan`) — walks a library path and stages
    unseen files into `bookdrop_items`, then enqueues a `bookdrop.ingest`
    job for each.
- The **SSE hub** (`internal/sse/`) broadcasts per-item updates; the browser
  subscribes via plain `EventSource` in `staticfs/static/sse.js`, which then
  calls `htmx.ajax()` to swap the affected row. No htmx `sse` extension
  required.
- Current job progress lives on the domain row itself (e.g.
  `bookdrop_items.state`/`progress`/`error_msg`) rather than a generic
  `task_history` table. A dedicated task-history table is deferred until we
  have multiple long-running job kinds to unify.

### 4.5 Authentication Flow

```
                    ┌──────────────┐
                    │   Client     │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
       ┌──────┴───┐ ┌─────┴────┐ ┌─────┴──────┐
       │ JWT Auth │ │   OIDC   │ │  Remote    │
       │ (local)  │ │ (extern) │ │  Auth      │
       └──────┬───┘ └─────┬────┘ └─────┬──────┘
              │            │            │
              └────────────┼────────────┘
                           │
                  ┌────────┴────────┐
                  │  auth middleware│
                  │ (gin.HandlerFunc)│
                  └────────┬────────┘
                           │
                  ┌────────┴────────┐
                  │  ctx.User(...)  │
                  │ (current user)  │
                  └─────────────────┘
```

- **Local session auth** *(built)* — `POST /login` verifies the password
  (bcrypt) and inserts a row into `sessions`. The session id (random UUID)
  rides in an `HttpOnly; SameSite=Lax` cookie. Every `/app/*` request runs
  `auth.RequireAuth`, which does a single `UPDATE sessions SET last_used_at =
  now() WHERE id=$1 AND expires_at > now() RETURNING ...` — sliding-window
  session in one query. 7-day TTL, slid forward on every request.
- **Basic Auth for OPDS** *(built)* — `/opds/*` uses HTTP Basic via
  `auth.BasicAuth`, since e-reader apps don't maintain session cookies.
  Credentials are verified against the same `users` table per request
  (`AuthService.Verify` — no session created).
- **First-run bootstrap** — `POST /signup` is open only while `users` is
  empty; the first account becomes admin. Afterwards `/signup` redirects to
  `/login` (admin invites a future slice).
- **OIDC** *(planned)* — `coreos/go-oidc` for discovery + `golang.org/x/oauth2`
  for the authorization-code flow. Backchannel logout endpoint to follow.
- **Remote/Forward Auth** *(planned)* — middleware that trusts
  `Remote-User` / `Remote-Email` / `Remote-Groups` reverse-proxy headers
  when `REMOTE_AUTH_ENABLED=true`.
- **Strict JWT** *(not planned for the web UI)* — server-side sessions fit
  the HTMX model better (free revocation, no refresh-token rotation
  ceremony). A JWT access-token layer may be added to `/api/v1/*` later if
  we ship a JSON API for external clients.

### 4.6 Error Handling

- Repo sentinels (`repo.ErrNotFound`, `repo.ErrAlreadyExists`,
  `repo.ErrLibraryPathTaken`) + service-layer sentinels
  (`service.ErrInvalidCredentials`, `service.ErrSignupClosed`,
  `service.ErrEmailTaken`, `service.ErrBadCoverURL`) let handlers branch
  on `errors.Is`.
- Handlers log with `log/slog` and return plain-text HTTP errors today.
  A dedicated `apierr` package + central error middleware (HTML vs JSON
  branching) is a planned cleanup when the surface area grows.
- Panics are recovered via `gin.Recovery()` and logged with `log/slog`.

---

## 5. Frontend Architecture

### 5.1 Server-Rendered + HTMX

The UI is a classic multi-page application enhanced with HTMX 2. There is no
JavaScript framework on the client; interactivity comes from:

- HTML attributes (`hx-get`, `hx-post`, `hx-swap`, `hx-target`, `hx-boost`,
  `hx-trigger`, `hx-push-url`, `hx-include`, `hx-indicator`).
- Small vanilla-JS modules (each ~40 lines, all in `internal/staticfs/static/`):
  - `sse.js` — one `EventSource` subscription to `/events`; dispatches
    `bookdrop.updated` events into `htmx.ajax()` row refreshes.
  - `enrich.js` — copies provider match fields into the metadata form on
    click (`[data-apply-match]` button).
  - `reader_epub.js` — wraps epub.js; reports CFI progress via `fetch` +
    `navigator.sendBeacon` on unload.
  - `reader_pdf.js` — wraps PDF.js with `IntersectionObserver`-based lazy
    page rendering and a `page:N` resume token.
- HTMX extensions (`sse`, `morph`, `preserve`, `path-deps`) are intentionally
  *not* wired — the 4 vanilla-JS shims above cover every live-update need
  without a second JS bundle.

### 5.2 Templ Component Structure

Each feature directory in `internal/view/` exposes:

- A **page** component (full document using the `app` layout) — the canonical full-render response.
- One or more **partials** (fragments) — targeted by HTMX swaps.
- Shared **components** (Cover, Sidebar, TopBar, Tweaks panel, etc.) live in `internal/view/component/`.

Handlers decide which variant to return based on the `HX-Request` header:

```go
if htmx.IsRequest(c.Request) {
    view.partial.LibraryGrid(books).Render(c.Request.Context(), c.Writer)
    return
}
view.page.Library(state, books).Render(c.Request.Context(), c.Writer)
```

### 5.3 Styling: Tailwind 4

- Tailwind 4's **CSS-first configuration** drives the design system. Tokens live in `web/src/styles.css`:

  ```css
  @import url('https://fonts.googleapis.com/css2?family=Source+Serif+4:ital,opsz,wght@...&family=Literata:ital,opsz,wght@...&family=IBM+Plex+Mono:wght@400;500;600&display=swap');
  @import "tailwindcss";
  @source "../../internal/view/**/*.templ";

  @theme {
    /* Archival paper neutrals — warm ivory, never yellow */
    --color-paper-0: oklch(0.985 0.006 85);
    --color-paper-1: oklch(0.965 0.010 82);
    /* Ink — navy-tinted near-black */
    --color-ink-1:   oklch(0.20 0.018 255);
    /* Library burgundy accent */
    --color-accent:  oklch(0.40 0.095 25);
    /* Library navy — secondary brand for chrome / status */
    --color-navy:    oklch(0.30 0.045 255);

    --font-serif:  "Source Serif 4", Georgia, serif;  /* UI + display */
    --font-reader: "Literata",       Georgia, serif;  /* long-form reading body */
    --font-mono:   "IBM Plex Mono",  ui-monospace, monospace;
  }
  ```

- **Source Serif 4** (Adobe, variable optical-sizing axis `opsz 8..60`) is
  the UI/display face; **Literata** (commissioned by Google for Play Books)
  is wired into `.reader-area` and `.book-edit-desc` via `--font-reader`.
- Utilities are generated at build time by the Tailwind 4 CLI watching
  `**/*.templ` (via the `@source` directive).
- A `@layer components` block defines the custom primitives —
  `.cover` / `.cov-*` palette swatches, `.match-card`, `.bdrop-row.state-*`,
  `.pdf-page`, `.settings-nav-item`, `.auth-card` — that are awkward to
  express as pure utilities.

### 5.4 Built-in Readers

| Reader | Status | Implementation |
|--------|--------|---------------|
| EPUB | **Built** | epub.js (vendored from pdfjs-dist sister project); server streams raw EPUB bytes; client handles pagination/typography; progress tracked as percent + CFI |
| PDF | **Built** | PDF.js UMD build (`pdfjs-dist@3.11.174/legacy`); lazy-renders pages via `IntersectionObserver`; progress tracked as percent + `page:N` resume token |
| CBX (.cbr / .cbz / .cb7) | Deferred | Server returns per-page image URLs; client viewer with keyboard nav + manga mode |
| Audiobook (MP3/M4B) | Deferred | Native `<audio>` with chapter navigation |

Progress lives in `user_book_progress` per user: `{percent, resume_cfi,
last_read_at}`. EPUB writes CFI strings (`epubcfi(...)`); PDF writes
`page:N` tokens. The prefix disambiguates them — one column suffices for
both reader types.

Typography preference (EPUB font-size) is stored in `localStorage` on the
client today; promoting it to `reader_preferences` is a trivial future
addition once the preference set grows.

---

## 6. Data Model

### 6.1 Core Domain Entities

Current schema — one row per migration below is tracked under
`internal/migrator/migrations/`:

```
libraries ──┬── library_paths (filesystem roots; scan stats)
            └── books ──┬── (title, author, format, year, rating, publisher,
                        │    series, series_index, tags[], isbn,
                        │    description, cover_palette, has_cover,
                        │    cover_mime, path, tsv (generated, GIN),
                        │    created_at, updated_at, deleted_at)
                        └── user_book_progress (user_id, progress, resume_cfi,
                             last_read_at)

bookdrop_items (ingest queue: path, format, state, title, author, description,
                language, has_cover, cover_mime, book_id on approve,
                discovered_at, updated_at)

users ──┬── sessions (id, expires_at, user_agent, last_used_at)
        └── shelves (per-user, unique(user_id, slug))
             └── shelf_books (shelf_id, book_id)

river_* tables — created by rivermigrate on boot.
```

Planned additions (not yet in migrations):

- `book_reviews`, `book_notes`, `annotations` (highlights/bookmarks)
- `reading_sessions` (time-spent analytics)
- `reader_preferences` (per-user, per-format)
- `audit_logs`, `user_content_restrictions`
- `email_providers`, `oidc_sessions`, `custom_fonts`
- `task_history` — unifying progress across job kinds once we have more than BookDrop + LibraryScan

Postgres-specific features used across the schema:

- `jsonb` for flexible per-user preference payloads.
- `tsvector` + GIN indexes for full-text search across title, author, description.
- Partial indexes (e.g., `WHERE deleted_at IS NULL`) for soft-deleted rows.
- `gen_random_uuid()` (pgcrypto) for primary keys where sortable IDs aren't needed.

### 6.2 Database Management

- **golang-migrate/migrate** manages schema evolution. Each migration is a
  pair of numbered SQL files in `internal/migrator/migrations/`
  (`NNNNNN_name.up.sql` and `NNNNNN_name.down.sql`). The driver is `postgres`
  via `pgx/v5`.
- Migrations are idempotent where practical (`CREATE ... IF NOT EXISTS`,
  `ADD COLUMN IF NOT EXISTS`).
- Released migrations are never modified; new migrations are created for changes.
- The app embeds the migration files (`//go:embed migrations/*.sql`) and
  runs `migrate.Up()` on boot by default. Opt out with
  `MIGRATE_ON_START=false` if migrations are managed externally via
  `go run ./cmd/migrate up`.
- River's own schema migrations are applied separately by `rivermigrate` at
  `queue.New` time.
- `sqlc` is staged via `sqlc.yaml` + `internal/repo/queries/*.sql` for a
  future typed-query pass; current repos use hand-written pgx.

---

## 7. External Integrations

### 7.1 Metadata Providers

| Provider | Status | Usage |
|----------|--------|-------|
| Google Books API | **Built** | Title, author, description, categories, cover, ISBN, publisher, year; no API key required for low-volume anonymous use |
| Open Library API | **Built** | Title, author, description, cover, ISBN, first-publish-year |
| Amazon | Deferred | Cover images + metadata scraping (requires affiliate ID or headless browser) |
| DuckDuckGo | Deferred | Cover image search fallback |

All providers implement the `provider.Provider` interface. `EnrichmentService`
fans queries across every provider concurrently via `errgroup.WithContext`;
one provider failing is logged but doesn't cancel the peers. Results are
merged and sorted by a confidence heuristic (`provider/score.go`) so the UI
shows the best matches first — auto-applying the top match is deliberately
avoided; the user always confirms.

Cover-fetch from a provider URL goes through `EnrichmentService.ImportCoverFromURL`
which **hard-enforces an allow-list of hosts** (`books.google.com`,
`books.googleusercontent.com`, `covers.openlibrary.org`), rejects non-HTTPS,
rejects non-`image/*` Content-Type, and caps body size at 10 MB — SSRF
protection baked in.

### 7.2 Device Sync Protocols

| Protocol/Device | Status | Integration Pattern |
|-----------------|--------|---------------------|
| OPDS 1.2 | **Built** | Atom/XML served at `/opds/*` with HTTP Basic Auth. Root nav + All / Library / Recent / Search acquisition feeds + OpenSearch description + per-book download/cover. Works with KOReader, Moon+ Reader, FBReader, Aldiko, Marvin, etc. |
| Kobo | Deferred | REST compatibility layer emulating Kobo's cloud endpoints. Deferred because the protocol is undocumented + proprietary; emulating it well is a multi-week reverse-engineering effort per-device. |
| KOReader | Deferred | REST sync API for reading progress |
| Hardcover.app | Deferred | REST API integration for reading-status sync |
| Komga | Deferred | REST API for comic-library import |

---

## 8. API Design

Three surface areas exist side by side:

- **HTML surface** — `/app/*` endpoints return Templ-rendered HTML. HTMX
  drives navigation via `hx-boost` on the app shell. Auth: cookie session
  (`auth.RequireAuth`).
- **OPDS surface** — `/opds/*` returns Atom XML for e-reader apps. Auth:
  HTTP Basic (`auth.BasicAuth`).
- **JSON surface** — `/api/v1/*` is healthcheck-only today; reserved for a
  future JSON API for external clients.

Shared concerns:

- **SSE endpoint:** `/events` (cookie-authed) — BookDrop state changes today;
  extensible for future task kinds.
- **Health check:** `GET /api/v1/healthcheck` (unauthenticated).
- **CSRF:** global Origin/Referer check on every state-changing request
  (`auth.CSRFGuard`) — paired with `SameSite=Lax` cookies.
- **File serving:** reader/OPDS stream book files through a path sandbox
  that allows only `BOOKDROP_PATH` + registered `library_paths` roots
  (trailing-separator prefix match).
- **File uploads:** streamed via `mime/multipart.Part.Read` — no full-body
  buffering for large archives. (No user-facing upload UI yet; files arrive
  via the `BOOKDROP_PATH` watcher or a `library.scan` job.)

### HTML Routes (cookie-auth under `/app/*`)

| Route | Notes |
|-------|-------|
| `GET /app` / `GET /app/` | Dashboard |
| `GET /app/library` | Library grid with `?q=`, `?sort=`, `?format=`, `?lib=` HTMX-swapped |
| `GET /app/book/:id` | Book detail — cover, metadata, progress slider, shelf toggles |
| `GET /app/book/:id/edit` | Metadata editor + **Find metadata online** enrichment panel |
| `POST /app/book/:id` | Save metadata edit (returns updated `#book-detail` panel for HTMX) |
| `POST /app/book/:id/progress` | Per-user progress update (form or JSON) |
| `POST /app/book/:id/shelf/:slug` | Toggle book on/off a shelf |
| `GET /app/book/:id/enrich` | Google Books + Open Library search results fragment |
| `POST /app/book/:id/cover-from-url` | Import a provider cover (allow-listed hosts) |
| `GET /app/read/:id` | Full-screen EPUB or PDF reader |
| `GET /app/read/:id/file` | Serves the book bytes (EPUB/PDF) via the path sandbox |
| `GET /app/cover/:id` | Book cover image |
| `GET /app/shelf/:slug` | Shelf contents |
| `POST /app/shelves` | Create shelf |
| `POST /app/shelf/:slug/delete` | Delete shelf |
| `GET /app/bookdrop` | Ingest review queue (SSE-live) |
| `GET /app/bookdrop/row/:id` | Single-row fragment (SSE swap target) |
| `GET /app/bookdrop/:id/cover` | Pre-approval cover preview |
| `POST /app/bookdrop/:id/approve\|reject` | Review queue action |
| `GET /app/settings` / `GET /app/settings/libraries` | Settings hub (admin) |
| `POST /app/settings/libraries/paths` | Register a new library filesystem root (admin) |
| `POST /app/settings/libraries/paths/:id/scan\|delete` | Trigger scan / remove path (admin) |
| `GET /events` | SSE stream |

### OPDS Routes (Basic-Auth under `/opds/*`)

| Route | Notes |
|-------|-------|
| `GET /opds/` | Navigation feed — All / Recent / per-library links |
| `GET /opds/all` | Acquisition feed (paged 50/page) |
| `GET /opds/library/:slug` | Per-library acquisition feed |
| `GET /opds/recent` | Recently added |
| `GET /opds/search?q=...` | Full-text search results |
| `GET /opds/search.xml` | OpenSearch description |
| `GET /opds/book/:id/download` | Book file bytes |
| `GET /opds/cover/:id` | Book cover image |

### Public Routes

| Route | Notes |
|-------|-------|
| `GET /` | 302 → `/app` |
| `GET /login` / `POST /login` | Session login |
| `POST /logout` | Destroy session + clear cookie |
| `GET /signup` / `POST /signup` | First-run bootstrap (admin); closes after the first user |
| `GET /static/*` | Embedded assets — `app.css`, `htmx.min.js`, readers, covers-from-JS |
| `GET /api/v1/healthcheck` | `{"status":"ok"}` |

---

## 9. Build and Deployment

### 9.1 Multi-Stage Docker Build

Three stages: Tailwind build → Go build (with `go tool templ generate`) →
distroless runtime. See [Dockerfile](Dockerfile) for the authoritative
recipe. Static assets (compiled Tailwind CSS, `htmx.min.js`, reader JS +
`epub.js`/`pdf.js` bundles, SSE/enrich shims) are embedded into the binary
via `//go:embed all:static` inside `internal/staticfs/`.

### 9.2 CI/CD Pipeline (planned)

No GitHub Actions workflows are committed yet. The intended shape:

```
develop-pipeline.yml
├── go vet && staticcheck
├── golang-migrate dry-run against throwaway Postgres
├── Backend tests (go test ./...)
├── E2E tests (Playwright against ephemeral server)
└── Build verification (go build)

master-pipeline.yml
├── All validation from develop
├── Docker image build
├── Push to Docker Hub (embookshelf/embookshelf)
└── Push to GHCR (ghcr.io/embookshelf-app/embookshelf)
```

### 9.3 Development Environment

```yaml
# compose.dev.yml
services:
  postgres:      # PostgreSQL 16 on :5432
  migrate:       # one-shot — `go run ./cmd/migrate up` inside a golang:alpine
```

The app itself runs on the host in dev (`make run` or `make dev` for
live-reload). `air` (via `go tool air`, no global install) rebuilds the Go
binary on any `.go` / `.templ` / `.sql` change and triggers
`go tool templ generate` first; `make css-watch` re-emits Tailwind on
template edits. Schema migrations apply automatically on boot
(`MIGRATE_ON_START=true` by default).

---

## 10. Security Architecture

| Concern | Status | Implementation |
|---------|--------|---------------|
| Authentication | **Built** | Local session cookies + OPDS Basic Auth (see §4.5). OIDC + Remote Auth deferred. |
| Authorization | **Built** | Gin middleware guards route groups (`RequireAuth`, `RequireRole`); admin-only gate on settings endpoints. Per-book ACL is a single-tenant model today — every user can see every book; deferred for multi-tenant content restrictions. |
| Password storage | **Built** | `golang.org/x/crypto/bcrypt`, min 8 chars. Seed admin hash generated via `pgcrypto.crypt(... gen_salt('bf'))` — same bcrypt format Go consumes. |
| Session management | **Built** | Server-side `sessions` table, 7-day TTL, slid forward in one atomic `UPDATE ... RETURNING`. Logout deletes the row. Opportunistic purge of expired sessions at boot. |
| CORS | **Built** | `gin-contrib/cors`, allowed origins via `ALLOWED_ORIGINS` env var. |
| CSRF | **Built** | Origin/Referer match against `Host` on every non-safe method (`auth.CSRFGuard`) paired with `SameSite=Lax` cookies. Per-form tokens not needed — all state-changing requests are same-origin. |
| File-serve sandbox | **Built** | Reader + OPDS download endpoints resolve the book path with `filepath.Abs` and require it to be rooted under `BOOKDROP_PATH` or a registered `library_paths` row (trailing-separator prefix check). Blocks `..`-traversal + prefix-match tricks. |
| Cover-fetch SSRF protection | **Built** | `EnrichmentService.ImportCoverFromURL` allow-lists provider hosts, requires HTTPS, requires `image/*` Content-Type, caps body at 10 MB. |
| Audit trail | Planned | `audit_logs` table for action history. |
| Parental controls | Planned | `user_content_restrictions`. |
| TLS | Runtime | Terminated by the reverse proxy in production; the server listens plain HTTP inside the container. OPDS Basic Auth assumes TLS. |

---

## 11. Configuration

All configuration flows through environment variables (optionally sourced from a `.env` file in development):

| Variable | Default | Purpose |
|----------|---------|---------|
| `EMBOOKSHELF_PORT` | `6060` | Application port |
| `DATABASE_URL` | `postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable` | Database connection |
| `DATABASE_MAX_CONNS` | `20` | pgxpool max connections |
| `DATABASE_MIN_CONNS` | `5` | pgxpool min idle connections |
| `MIGRATE_ON_START` | `true` | Apply pending app migrations on boot. Set `false` to manage externally via `go run ./cmd/migrate up`. |
| `DISK_TYPE` | `LOCAL` | Storage mode (`LOCAL` — read/write; `NETWORK` — read-only for NAS/NFS/SMB mounts). |
| `BOOKDROP_PATH` | `./bookdrop` | Watched folder for manual imports. |
| `BOOKDROP_POLL_SECONDS` | `5` | Watcher poll interval. |
| `DATA_PATH` | `./data` | Storage root for derived data — covers under `${DATA_PATH}/covers/books/` and `bookdrop/`. |
| `ALLOWED_ORIGINS` | `*` | CORS origins (comma-separated). |
| `LOG_LEVEL` | `info` | `slog` level. |
| `REMOTE_AUTH_ENABLED` | `false` | *(planned)* Enable reverse-proxy auth. |
| `SESSION_SECRET` | — | *(reserved)* Not read today — sessions are server-side. Will be used when a JWT layer is added for the JSON API. |

Library filesystem roots beyond `BOOKDROP_PATH` are managed in the database
(`library_paths` table via the Settings → Libraries UI), not via env.

---

## 12. Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Monolith over microservices** | Single deployment unit simplifies self-hosting; no inter-service communication overhead |
| **Go over JVM** | Lower memory footprint, single static binary, faster cold starts — all meaningful for self-hosters on modest hardware |
| **Templ + HTMX over SPA** | Server-rendered HTML ships less JavaScript, simplifies auth flows, keeps the entire UI inspectable from the network tab, and eliminates the API/UI type-duplication tax |
| **HTMX 4 SSE over custom WebSocket protocol** | SSE is one-way (server → client) which matches every real-time need here (task progress, library scan updates); HTMX 4's `sse` extension wires it into the DOM declaratively |
| **Tailwind 4 CSS-first config** | Design tokens live in one `@theme` block that both CSS and Templ components reference — no JS config, no PostCSS plugin drift |
| **PostgreSQL over MariaDB/SQLite** | `jsonb`, `tsvector` full-text search, and a mature job-queue ecosystem (river) more than earn the operational overhead |
| **sqlc over ORM** | Typed, compile-time-checked SQL keeps the query surface explicit and avoids N+1 surprises |
| **golang-migrate over goose/dbmate** | Paired `.up.sql`/`.down.sql` files are unambiguous; the library is small, pgx-friendly, and can be embedded into the app binary so a single artifact can run its own migrations in any environment |
| **Gin over chi/echo** | Rich built-in middleware (logger, recovery, CORS via `gin-contrib`), well-known binding/validation story, and ergonomic `gin.Context` for streaming Templ output into the response writer |
| **river over custom worker pool** | Jobs live in the same Postgres transaction boundary as the mutations that enqueue them; exactly-once semantics without extra infrastructure |
| **Format-specific processors** | Strategy pattern allows adding new formats without modifying existing code |
| **NETWORK storage mode** | Safe degradation for NAS users rather than risking file corruption |
| **Server-side sessions over stateless JWT (for the web UI)** | HTMX + SameSite cookies are the natural fit; revocation is free, no refresh-token rotation ceremony. A JWT layer can still be added to `/api/v1/*` later without touching the web auth. |
| **Basic Auth for OPDS** | E-reader apps don't carry session cookies; Basic Auth over TLS is the documented pattern and works with every OPDS client out there. |
| **Client-side EPUB/PDF readers** | Browser-native pagination + typography + IntersectionObserver-based lazy rendering beats server-side reflow at the implementation-cost/quality tradeoff point. The server's job stays tight: serve bytes, persist position. |
| **Path-rooted file-serve sandbox** | Single authoritative list (BOOKDROP + registered library paths) beats per-book ACL for a self-hosted single-tenant instance; adding per-book ACLs later is an additive check, not a replacement. |
| **CFI + `page:N` resume tokens share one DB column** | Unambiguous by prefix (`epubcfi(...)` vs `page:42`); avoids a parallel column or JSONB for a two-format reader today. |
| **`Deps` struct for `handler.New`** | 11+ dependencies in a positional constructor was unreadable; struct literal makes call sites self-documenting and new deps additive. |
| **Source Serif 4 + Literata + IBM Plex Mono** | Research-backed editorial stack: Source Serif 4 (Adobe, variable `opsz 8..60`, screen-tuned) for UI + display; Literata (commissioned by Google for Play Books) for the reader body via `--font-reader`; IBM Plex Mono for metadata chrome. |
