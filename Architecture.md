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
| **HTTP Router** | chi | 5.x |
| **Templating** | Templ | 0.3+ |
| **Interactivity** | HTMX | 4.x |
| **Styling** | Tailwind CSS | 4.x |
| **Database** | PostgreSQL | 16+ |
| **DB Driver** | pgx (v5) | 5.x |
| **Query Codegen** | sqlc | 1.x |
| **Migrations** | goose | 3.x |
| **Auth** | golang-jwt + coreos/go-oidc | — |
| **Sessions** | alexedwards/scs + scs/pgxstore | — |
| **Background Jobs** | river (Postgres-backed queue) | 0.x |
| **Cache** | ristretto | 1.x |
| **Real-time** | Server-Sent Events via chi handler | — |
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
│   └── embookshelf/                # main.go — composition root
│
├── internal/
│   ├── handler/                    # HTTP handlers (HTMX-aware)
│   │   ├── book.go
│   │   ├── library.go
│   │   ├── reader.go
│   │   ├── shelf.go
│   │   ├── metadata.go
│   │   ├── bookdrop.go
│   │   ├── notebook.go
│   │   ├── settings.go
│   │   └── auth.go
│   ├── service/                    # Business logic
│   ├── repo/                       # pgx/sqlc-backed repositories
│   │   ├── queries/                # .sql files consumed by sqlc
│   │   └── generated/              # sqlc output (do not edit)
│   ├── model/                      # Domain structs & enums
│   ├── view/                       # .templ files — pages, partials, components
│   │   ├── layout/                 # shells (app, reader, auth)
│   │   ├── page/                   # full-page views
│   │   ├── partial/                # HTMX swap targets
│   │   └── component/              # reusable (Cover, Sidebar, TopBar, ...)
│   ├── middleware/                 # auth, logging, htmx-detect, csrf
│   ├── task/                       # river job handlers (scans, enrichment)
│   ├── fileproc/                   # Format-specific processors
│   │   ├── pdf.go                  # pdfcpu
│   │   ├── epub.go                 # kapmahc/epub
│   │   ├── cbx.go                  # archive/zip + rar
│   │   ├── audiobook.go            # dhowden/tag
│   │   ├── azw3.go
│   │   ├── mobi.go
│   │   └── fb2.go
│   ├── provider/                   # External metadata providers
│   ├── auth/                       # JWT, OIDC, remote-auth strategies
│   ├── sse/                        # Server-Sent Events hub
│   └── config/                     # env + yaml loading
│
├── migrations/                     # goose SQL migrations (*.sql)
├── web/
│   ├── src/
│   │   ├── styles.css              # Tailwind 4 entry (@import "tailwindcss")
│   │   └── reader/                 # PDF.js, epub.js wrappers, etc.
│   └── static/                     # compiled assets (htmx.min.js, app.css, readers)
│
├── scripts/                        # dev tooling
├── Dockerfile                      # Multi-stage production build
├── compose.dev.yml                 # Development environment
├── compose.example.yml             # Production Docker Compose example
├── deploy/helm/                    # Helm chart for Kubernetes
├── .github/workflows/              # CI/CD pipelines
├── go.mod
├── go.sum
└── package.json                    # tailwind + htmx build tooling
```

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

- **Handlers** — HTTP endpoints under `/app/*` (HTML) and `/api/v1/*` (JSON, used by external clients and e-reader protocols). HTMX requests are detected via the `HX-Request` header; handlers return full pages on direct navigation and partials on HTMX swaps.
- **Services** — Business logic. Plain Go structs wired with constructor functions in `cmd/embookshelf/main.go`.
- **Repositories** — `pgx` + `sqlc`-generated query methods. Raw SQL lives in `internal/repo/queries/*.sql`.
- **Views** — Templ components render responses. A page handler composes a layout + page component; a partial handler returns a fragment targeting a specific HTMX swap.
- **DTOs** — Request/response structs are colocated with handlers. Database row types from sqlc never leak past the repository boundary.

### 4.2 Concurrency Model

- **Goroutines** handle every request; all I/O (DB, file, HTTP) is naturally non-blocking on the Go scheduler.
- **pgxpool** connection pool: 20 max connections, 5 min idle (tunable via env).
- **ristretto** for in-memory caching (cover thumbnails, session lookups, provider responses).
- `context.Context` is threaded through every call and respects request cancellation.

### 4.3 File Processing Pipeline

Each format has a dedicated processor implementing a common interface:

```go
type Processor interface {
    Extract(ctx context.Context, path string) (Metadata, error)
    Cover(ctx context.Context, path string) (image.Image, error)
    Outline(ctx context.Context, path string) ([]Chapter, error)
}
```

```
fileproc.Processor
├── PDFProcessor        — pdfcpu
├── EPUBProcessor       — kapmahc/epub
├── CBXProcessor        — archive/zip, nwaples/rardecode
├── AudiobookProcessor  — dhowden/tag
├── AZW3Processor
├── MOBIProcessor
└── FB2Processor
```

Processors extract embedded metadata, cover images, table of contents, and page/track counts.

### 4.4 Async Task System

Background work (library scans, metadata fetches, BookDrop processing) uses **river**, a Postgres-backed job queue:

- Jobs are enqueued in the same transaction as the triggering mutation (exactly-once handoff).
- `internal/task/` contains per-job handlers.
- Progress is written to a `task_history` table.
- The **SSE hub** broadcasts task updates; HTMX 4's `hx-ext="sse"` subscribes relevant UI regions and swaps progress bars / status chips in place.

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
                  │ (chi.Middleware)│
                  └────────┬────────┘
                           │
                  ┌────────┴────────┐
                  │  ctx.User(...)  │
                  │ (current user)  │
                  └─────────────────┘
```

- **JWT** — Local login produces access + refresh tokens signed with per-instance keys stored in `jwt_secret`. `golang-jwt/jwt` handles signing/validation.
- **OIDC** — `coreos/go-oidc` performs discovery; `golang.org/x/oauth2` drives the authorization-code flow. Backchannel logout supported via a dedicated endpoint.
- **Remote Auth** — `RemoteAuthMiddleware` reads identity from reverse-proxy headers (`Remote-User`, `Remote-Email`, `Remote-Groups`) when `REMOTE_AUTH_ENABLED=true`.

### 4.6 Error Handling

- `apierr` package defines domain errors with matching HTTP status codes.
- Central `errorMiddleware` converts errors into either JSON (for `/api/*`) or an inline Templ error component targeting `#flash` (for HTMX responses).
- Panics are recovered and logged with `log/slog`.

---

## 5. Frontend Architecture

### 5.1 Server-Rendered + HTMX 4

The UI is a classic multi-page application enhanced with HTMX. There is no JavaScript framework on the client; interactivity comes from:

- HTML attributes (`hx-get`, `hx-post`, `hx-swap`, `hx-target`, `hx-boost`).
- HTMX 4 extensions: `sse` for real-time task progress, `preserve` for reader scroll position, `path-deps` for reactive URL state, and `morph` for minimal DOM diffing on partial swaps.
- Small vanilla-JS modules for things that belong on the client: PDF.js reader, EPUB reflow engine, audio player, drag-and-drop file uploads.

### 5.2 Templ Component Structure

Each feature directory in `internal/view/` exposes:

- A **page** component (full document using the `app` layout) — the canonical full-render response.
- One or more **partials** (fragments) — targeted by HTMX swaps.
- Shared **components** (Cover, Sidebar, TopBar, Tweaks panel, etc.) live in `internal/view/component/`.

Handlers decide which variant to return based on the `HX-Request` header:

```go
if htmx.IsRequest(r) {
    view.partial.LibraryGrid(books).Render(r.Context(), w)
    return
}
view.page.Library(state, books).Render(r.Context(), w)
```

### 5.3 Styling: Tailwind 4

- Tailwind 4's **CSS-first configuration** drives the design system. Tokens live in `web/src/styles.css`:

  ```css
  @import "tailwindcss";

  @theme {
    --color-paper-0: oklch(0.98 0.008 80);
    --color-paper-1: oklch(0.96 0.012 78);
    --color-ink-1: oklch(0.22 0.015 60);
    --color-accent: oklch(0.48 0.09 35);
    --font-display: "Spectral", Georgia, serif;
    --font-mono: "JetBrains Mono", ui-monospace, monospace;
  }
  ```

- Utilities are generated at build time by the Tailwind 4 CLI watching `**/*.templ`.
- One custom layer (`@layer components`) defines shelf-plank, book-cover, and drop-cap primitives that are awkward to express as pure utilities.

### 5.4 Built-in Readers

| Reader | Implementation |
|--------|---------------|
| PDF | PDF.js (embedded as a static asset) with a custom annotation layer that `POST`s highlights back via HTMX |
| EPUB | Server streams a reflowable HTML rendering; a small client script handles pagination/typography tweaks |
| CBX | Server returns per-page image URLs; client viewer supports keyboard nav and manga mode |
| Audiobook | Native `<audio>` element with chapter navigation driven by HTMX-loaded track lists |

Per-user preferences (zoom, theme, font, layout, playback speed) are persisted server-side and rendered into the page on load.

---

## 6. Data Model

### 6.1 Core Domain Entities

```
libraries ──┬── library_paths (filesystem paths)
            └── books ──┬── book_files (actual files: PDF, EPUB, etc.)
                        ├── book_metadata (title, description, ratings)
                        ├── book_additional_files (supplementary files)
                        ├── book_authors (m2m)
                        ├── bookmarks
                        ├── book_notes
                        ├── book_reviews
                        ├── annotations (PDF annotations)
                        ├── comic_metadata
                        └── user_book_progress

users ──┬── user_permissions (role-based access)
        ├── user_settings (preferences)
        ├── shelves ─── shelf_books (m2m)
        ├── reading_sessions
        ├── refresh_tokens
        └── reader_preferences (PDF, EPUB, CBX)

system ──┬── app_settings (global config)
         ├── audit_logs (action trail)
         ├── task_history (background job records)
         ├── email_providers
         ├── oidc_sessions
         └── custom_fonts
```

Postgres-specific features used across the schema:

- `jsonb` for flexible per-user preference payloads.
- `tsvector` + GIN indexes for full-text search across title, author, description.
- Partial indexes (e.g., `WHERE deleted_at IS NULL`) for soft-deleted rows.
- `gen_random_uuid()` (pgcrypto) for primary keys where sortable IDs aren't needed.

### 6.2 Database Management

- **goose** manages schema evolution (numbered SQL files in `migrations/`).
- Migrations are idempotent (guards: `CREATE ... IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS` via `DO $$` blocks).
- Released migrations are never modified; new migrations are created for changes.
- Connection timezone forced to UTC (`TimeZone=UTC` in `DATABASE_URL`).
- `sqlc` generates typed query methods from the SQL in `internal/repo/queries/`.

---

## 7. External Integrations

### 7.1 Metadata Providers

| Provider | Usage |
|----------|-------|
| Google Books API | Title, author, description, categories, cover, ISBN |
| Open Library API | Title, author, description, cover |
| Amazon | Cover images, metadata scraping |
| DuckDuckGo | Cover image search fallback |

All providers implement a common `metadata.Provider` interface and are called concurrently via `errgroup.Group`; the first confident match wins, with the rest surfaced as alternatives in the metadata editor.

### 7.2 Device Sync Protocols

| Protocol/Device | Integration Pattern |
|-----------------|-------------------|
| Kobo | REST API compatibility layer emulating Kobo's cloud endpoints |
| KOReader | REST sync API for reading progress |
| OPDS | Atom/XML feed protocol for e-reader app compatibility |
| Hardcover.app | REST API integration for reading status sync |
| Komga | REST API for comic library import |

---

## 8. API Design

Two surface areas exist side by side:

- **HTML surface** — `/app/*` endpoints return Templ-rendered HTML. HTMX drives navigation via `hx-boost` on the app shell.
- **JSON surface** — `/api/v1/*` endpoints return JSON for external clients and device-sync protocols.

Shared concerns:

- **SSE endpoint:** `/events` for real-time task progress (HTMX `sse` extension).
- **OPDS feed:** `/opds/` for e-reader protocol.
- **Kobo sync:** `/kobo/` endpoint group emulating Kobo cloud API.
- **Health check:** `GET /api/v1/healthcheck`.
- **Pagination:** cursor-based, max 100 items per page.
- **File uploads:** streamed to disk via `mime/multipart`'s `Part.Read` to avoid buffering large archives.

### Key HTML Routes

| Group | Path Prefix | Notes |
|-------|-------------|-------|
| Dashboard | `/app` | Default view |
| Library | `/app/library` | Shelf / grid / list layouts, HTMX swap on filter/sort |
| Book Detail | `/app/book/{id}` | Tabs swap via HTMX |
| Reader | `/app/read/{id}` | Full-screen takeover; in-reader actions HTMX-driven |
| Metadata | `/app/book/{id}/edit` | Form submits return updated fragment |
| BookDrop | `/app/bookdrop` | SSE updates queue rows |
| Notebook | `/app/notebook` | Search box with `hx-trigger="keyup changed delay:200ms"` |
| Settings | `/app/settings/*` | Section panels swapped into main area |

### Key JSON Routes

| Group | Path Prefix |
|-------|-------------|
| Books | `/api/v1/book` |
| Readers | `/api/v1/reader` |
| Libraries | `/api/v1/library` |
| Shelves | `/api/v1/shelf` |
| Metadata | `/api/v1/metadata` |
| Users | `/api/v1/user` |
| Device Sync | `/api/v1/kobo`, `/api/v1/koreader` |
| Settings | `/api/v1/setting` |
| Import | `/api/v1/bookdrop` |

---

## 9. Build and Deployment

### 9.1 Multi-Stage Docker Build

```dockerfile
# Stage 1: Compile CSS + generate Templ
FROM node:22-alpine AS assets
RUN npm ci
RUN npx tailwindcss -i web/src/styles.css -o web/static/app.css --minify

FROM ghcr.io/a-h/templ:latest AS templ
RUN templ generate

# Stage 2: Build Go binary (embeds web/static via //go:embed)
FROM golang:1.23-alpine AS api-build
WORKDIR /src
COPY --from=assets /app/web/static ./web/static
COPY --from=templ /src/internal/view ./internal/view
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/embookshelf ./cmd/embookshelf

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12
COPY --from=api-build /out/embookshelf /embookshelf
ENTRYPOINT ["/embookshelf"]
```

Static assets (compiled Tailwind CSS, `htmx.min.js`, reader bundles) are embedded into the binary via `//go:embed web/static`.

### 9.2 CI/CD Pipeline (GitHub Actions)

```
develop-pipeline.yml
├── go vet && staticcheck
├── goose migration dry-run against throwaway Postgres
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
  embookshelf:   # Go binary with air for live reload on :6060
                 # tailwindcss --watch + templ generate --watch in sidecar
  postgres:      # PostgreSQL 16 on :5432
```

Local iteration loop: `air` rebuilds the Go binary on any `.go` change; `templ generate --watch` regenerates view code on `.templ` edits; `tailwindcss --watch` rebuilds `app.css` on template or CSS changes.

---

## 10. Security Architecture

| Concern | Implementation |
|---------|---------------|
| Authentication | JWT + OIDC + Remote Auth (see Section 4.5) |
| Authorization | `chi.Middleware` guards per route group; per-book ACL checked inside services via `auth.CanAccessBook(ctx, bookID)` |
| Password storage | `golang.org/x/crypto/bcrypt` |
| Token management | Short-lived access tokens + rotating refresh tokens |
| CORS | `go-chi/cors`, allowed origins via `ALLOWED_ORIGINS` env var |
| CSRF | `gorilla/csrf` on every state-changing form; HTMX sends the token via `hx-headers` |
| Audit trail | `audit_logs` table records user actions |
| Content restriction | `user_content_restrictions` table for parental controls |
| TLS | Terminated by reverse proxy in production; server listens plain HTTP inside the container |

---

## 11. Configuration

All configuration flows through environment variables (optionally sourced from a `.env` file in development):

| Variable | Default | Purpose |
|----------|---------|---------|
| `EMBOOKSHELF_PORT` | 6060 | Application port |
| `DATABASE_URL` | `postgres://embookshelf:embookshelf@postgres:5432/embookshelf?sslmode=disable` | Database connection |
| `DATABASE_MAX_CONNS` | 20 | pgxpool max connections |
| `DATABASE_MIN_CONNS` | 5 | pgxpool min idle connections |
| `DISK_TYPE` | LOCAL | Storage mode (LOCAL/NETWORK) |
| `ALLOWED_ORIGINS` | * | CORS origins |
| `REMOTE_AUTH_ENABLED` | false | Enable reverse proxy auth |
| `FORCE_DISABLE_OIDC` | false | Disable OIDC |
| `SESSION_SECRET` | — | Signing key for session cookies |
| `USER_ID` / `GROUP_ID` | — | Container filesystem ownership |

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
| **goose over dbmate/liquibase** | Simple numbered SQL files; tooling ships as a single Go binary that can be run in CI and in the app itself |
| **river over custom worker pool** | Jobs live in the same Postgres transaction boundary as the mutations that enqueue them; exactly-once semantics without extra infrastructure |
| **Format-specific processors** | Strategy pattern allows adding new formats without modifying existing code |
| **NETWORK storage mode** | Safe degradation for NAS users rather than risking file corruption |
