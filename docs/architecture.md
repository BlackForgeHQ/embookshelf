# Embookshelf - Architecture Document

## 1. High-Level Architecture

Embookshelf is a Go backend + React SPA, shipped as a single binary. The
UI is a [TanStack Start](https://tanstack.com/start) app compiled in
**SPA mode** ([Vite 8](https://vite.dev) + React 19 + TypeScript +
[Tailwind 4](https://tailwindcss.com) + [shadcn/ui](https://ui.shadcn.com)).
The Go server exposes JSON APIs under `/api/v1/*`, an OPDS 1.2 catalog
under `/opds/*`, an SSE stream at `/events`, and serves the compiled SPA
shell embedded in the binary via `//go:embed`. Data lives in PostgreSQL.

```
┌───────────────────────────────────────────────────────────┐
│                       Docker Container                     │
│                                                           │
│  ┌─────────────────────────────────────────────────────┐  │
│  │               Go server (port 6060)                  │  │
│  │                                                     │  │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────┐ │  │
│  │  │ /api/v1  │  │  /opds   │  │  /events │  │ SPA │ │  │
│  │  │  (JSON)  │  │ (Atom    │  │  (SSE)   │  │ /   │ │  │
│  │  │          │  │  + Basic │  │          │  │ (em │ │  │
│  │  │          │  │  Auth)   │  │          │  │ bed)│ │  │
│  │  └────┬─────┘  └────┬─────┘  └────┬─────┘  └──┬──┘ │  │
│  │       │             │             │            │    │  │
│  │  ┌────┴─────────────┴─────────────┴────────┐   │    │  │
│  │  │           Service Layer                  │   │    │  │
│  │  └────┬─────────────────────────────────┬──┘   │    │  │
│  │       │                                 │      │    │  │
│  │  ┌────┴────┐  ┌─────────┐  ┌────────┐   │  ┌───┴──┐ │  │
│  │  │  pgx    │  │ File IO │  │External│   │  │stati │ │  │
│  │  │  (repo) │  │ (books) │  │  APIs  │   │  │cfs/  │ │  │
│  │  └────┬────┘  └─────────┘  └────────┘   │  │dist/ │ │  │
│  └───────┼──────────────────────────────────┴──┴──────┴──┘
│          │
└──────────┼────────────────────────────────────────────────┘
           │
    ┌──────┴──────┐
    │  PostgreSQL │
    │    16+      │
    └─────────────┘

                    ┌─────────────────────┐
   Browser ───────► │  React SPA (TanStack│
   (cookies)        │  Start, SPA mode)   │
                    │                     │
                    │ @tanstack/react-    │
                    │   router (file-     │
                    │   based routes)     │
                    │ @tanstack/react-    │
                    │   query (server-    │
                    │   state cache)      │
                    └─────────────────────┘
```

The browser always loads the embedded `index.html` (TanStack Start's
`_shell.html` duplicated at that path). Every unmatched GET falls back to
the shell via Gin's `NoRoute` handler so client-side routing works on
hard reloads. Data fetches go through `fetch()` with `credentials: 'include'`
so the session cookie the Go server issues rides along.

---

## 2. Technology Stack

### Backend

| Layer | Technology | Version |
|-------|-----------|---------|
| **Runtime** | Go | 1.25 |
| **HTTP Router** | Gin ([gin-gonic/gin](https://github.com/gin-gonic/gin)) | 1.x |
| **Database** | PostgreSQL — the only supported backend (ADR-0023) | 16+ |
| **DB Driver** | pgx | v5 |
| **Migrations** | [golang-migrate/migrate](https://github.com/golang-migrate/migrate) | 4.x |
| **Sessions** | Hand-rolled `sessions` table (see §4.5) | — |
| **Passwords** | `golang.org/x/crypto/bcrypt` | — |
| **Background Jobs** | [riverqueue](https://riverqueue.com/) — Postgres-backed | 0.34 |
| **Real-time** | Server-Sent Events via Gin handler | — |
| **Live reload (dev)** | [air-verse/air](https://github.com/air-verse/air) via `go tool air` | 1.65 |
| **Containerization** | Docker (multi-stage) | — |

### UI

| Layer | Technology | Version |
|-------|-----------|---------|
| **Framework** | [TanStack Start](https://tanstack.com/start) (SPA mode) | 1.167+ |
| **Router** | [@tanstack/react-router](https://tanstack.com/router) (file-based routes) | 1.x |
| **Server state** | [@tanstack/react-query](https://tanstack.com/query) | 5.59+ |
| **UI runtime** | React + ReactDOM | 19.2 |
| **Language** | TypeScript | 5.9 |
| **Bundler** | [Vite](https://vite.dev) | 8.x |
| **Styling** | Tailwind CSS 4 via `@tailwindcss/vite` plugin (first-class, no CLI side-process) | 4.2 |
| **Component library** | [shadcn/ui](https://ui.shadcn.com) (radix-mira style) over [radix-ui](https://www.radix-ui.com) primitives | 4.4 |
| **Icons** | [lucide-react](https://lucide.dev) (shadcn default) + a 46-icon bespoke set (`Icon.tsx`) | 1.8 |
| **Toasts** | [sonner](https://sonner.emilkowal.ski/) via shadcn wrapper | 2.0 |
| **Package manager / runner** | [Bun](https://bun.sh) — `bun install` / `bun run dev` / `bun run build` | 1.x |
| **Browser telemetry** | `@opentelemetry/sdk-trace-web` + `document-load` / `user-interaction` / `fetch` instrumentations, gated on `VITE_OTEL_ENABLED=true` | 2.7 / 0.215 |

Testing stacks: Go `testing` + `testify` for the backend; Playwright
for end-to-end browser coverage under [e2e/](../e2e/) — see
[ADR-0006](./adr/0006-playwright-e2e-against-built-binary.md). UI unit tests
are wired with **Vitest** + **@testing-library/react** (`bun run test`
inside `ui/`).

### Database backend

embookshelf runs against Postgres, named by `DATABASE_URL`. It is the only
supported runtime database — see
[ADR-0023](./adr/0023-postgres-only.md). A `sqlite://` DSN refuses to boot
with a message pointing at `embookshelf import-sqlite`, the one-shot
importer that moves an existing SQLite library into Postgres (§6.2).

SQLite was a supported backend until 2026-07; the dual-dialect design
spec from that era (`docs/superpowers/specs/2026-04-28-sqlite-support-design.md`)
is historical and superseded by ADR-0023, not current design guidance.

---

## 3. Project Structure

```
embookshelf/
├── cmd/
│   ├── embookshelf/                # main.go — composition root
│   └── migrate/                    # CLI around internal/migrator (up/down/version/force)
│
├── internal/                       # Backend packages — tiered below
├── ui/                             # TanStack Start SPA (Vite + React 19 + Tailwind 4 + shadcn/ui)
├── e2e/                            # Playwright tests (separate Node project — see ADR-0006)
├── scripts/seed.sql                # Dev seed — admin@local / changeme
├── docs/                           # ARCHITECTURE.md + prd.md + adr/ + spec/ + superpowers/
├── Dockerfile                      # Multi-stage (bun build ui → go build → distroless)
├── compose.dev.yml                 # Development Postgres + grafana/otel-lgtm
├── compose.prod.yml                # Production reference compose with S3 env wiring
├── go.mod                          # Go 1.25; `tool` directive for air
├── Makefile                        # db-up, ui-install, dev, ui-dev, up, build, ...
├── .air.toml                       # live-reload config (excludes ui/ and dist/)
└── README.md
```

### `internal/` — tiered

The backend has 27 packages. Tiered by role:

**Core (request → domain → persistence)**
- `handler/` — Gin `HandlerFunc`s; one file per resource group, plus
  `router.go` (route assembly + SPA fallback), `handler.go`
  (`Handler` + `Deps`), `errjson.go` (JSON error envelope).
- `service/` — Business logic. ~20 services (see §4.1).
- `repo/` — Hand-written SQL via pgx. Single dialect: one Postgres
  query text per statement (ADR-0023).
- `model/` — Domain structs and shared enums (`Role`, `DeviceKind`,
  `EditableMetadata`, etc.).
- `migrator/` — Embedded `golang-migrate` wrapper. `migrations/postgres/`
  is the live schema. `migrations/sqlite/` survives only to bring an old
  source database forward before `import-sqlite` reads it (ADR-0023).

**IO (storage + format)**
- `storage/` — Backend-agnostic blob interface (`Storage`, capability
  bits, `Source` random-access reader). Subpackages:
  - `local/` — POSIX filesystem implementation.
  - `s3/` — AWS SDK v2 implementation with presign + iter helpers.
  - `storagetest/` — shared conformance tests both backends pass.
- `storageloader/` — Boot-time helper that reads `storage_backends`
  rows and returns a `storage.Resolver` (per-library lookup; first
  row is the default).
- `coverstore/` — Filesystem store for extracted/imported cover
  images, written under `${DATA_PATH}/covers/`. Hashed dedup
  (SHA-256) since migration `000027`.
- `fileproc/` — Per-format extract + embed processors.
  `processor.go` (interface + `Dispatch`), `epub.go` + `epub_embed.go`,
  `pdf.go` + `pdf_embed.go`, `cbz.go`, `audio.go`, `embedder.go`.
- `extractor/` — Format-agnostic metadata façade used by services that
  don't care which processor backs a given path.
- `sidecar/` — OPF/JSON sidecar reader + writer. ADR-0001
  (edit-side write-back). Reattach on rescan reads sidecars to
  preserve user edits.
- `scan/` — Filesystem walker, classifier, differ, reattach,
  cover-pick. Powers the `library.scan` job (ADR-0003 book-per-folder
  layout, ADR-0004 scan auto-imports).
- `ingest/` — `BOOKDROP_PATH` watcher (polling) that stages dropped
  files into `bookdrop_items`.
- `layout/` — Filename sanitization for written files.
- `tagging/` — Tag normalization shared by enrichment + sidecar reads.
- `hashing/` — SHA-256 helpers for content + cover dedup.

**Cross-cutting**
- `auth/` — Password (bcrypt), session cookie helpers, request
  context (`UserFromContext`), `RequireAuth` / `RequireRole` /
  `BasicAuth` / `CSRFGuard` middleware. OIDC service lives in
  `service/oidc.go`, not here.
- `config/` — Env loading (`config.go`) + per-storage typed config
  (`storage.go`).
- `crypto/` — AES-256-GCM helpers for provider secret encryption at
  rest (ADR-0010).
- `db/` — `*sql.DB` wrapper over the pgx pool, DSN detection (a
  `sqlite://` DSN is recognized only to refuse it), shared scan helpers.
  `sqlite_driver.go` registers `modernc.org/sqlite` for the importer's
  read-only path and nothing else.
- `queue/` — One-method `Client` interface over River. `registry.go`
  declares each job type once (kind + args + work fn) and derives
  River's typed-worker plumbing from it. See §4.4.
- `task/` — Job kinds + workers (`BookDropWorker`,
  `LibraryScanWorker`, `ScanImportWorker`).
- `sqliteimport/` — Read-only SQLite → Postgres importer behind
  `embookshelf import-sqlite`. Deletable when the deprecation window
  closes (ADR-0023).
- `sse/` — Fan-out hub for the `/events` endpoint.
- `opds/` — Atom/XML feed types + builder.
- `provider/` — External metadata sources + catalog + resilient
  client + scoring. See §7.1.
- `staticfs/` — `//go:embed all:dist` host for the compiled SPA.
- `telemetry/` — OTLP exporter wiring. Gated on `OTEL_ENABLED`.

### `ui/` — frontend layout

```
ui/
├── src/
│   ├── routes/                     # File-based; routeTree.gen.ts auto-generated
│   │   ├── __root.tsx              # html/body shell (HeadContent + Scripts + QueryClient + Toaster)
│   │   ├── _app.tsx                # Pathless layout: Sidebar + main + status bar
│   │   ├── _app.index.tsx          # /                  — Dashboard
│   │   ├── _app.library.tsx        # /library           — LibraryView (shelf|grid|list)
│   │   ├── _app.book.$id.tsx       # /book/:id          — BookDetail
│   │   ├── _app.book.$id_.edit.tsx # /book/:id/edit     — MetadataEditor
│   │   ├── _app.book.$id_.find.tsx # /book/:id/find     — Provider match search
│   │   ├── _app.notebook.tsx       # /notebook          — cross-book notes
│   │   ├── _app.bookdrop.tsx       # /bookdrop          — import review queue
│   │   ├── _app.stats.tsx          # /stats             — library + reading statistics
│   │   ├── _app.account.tsx        # /account           — account hub (identities, password)
│   │   ├── _app.settings.tsx       # /settings          — admin hub
│   │   ├── read.$id.tsx            # /read/:id          — full-screen Reader (no sidebar)
│   │   ├── login.tsx               # /login             — local + first-run signup + OIDC
│   │   └── login.pending.tsx       # /login/pending     — awaiting-admin-approval gate
│   ├── components/                 # See §5.4 for grouping
│   ├── api/                        # Typed clients per resource (auth, account, books, oidc, …)
│   ├── data/mock.ts                # Reference dataset — types only; live routes hit the API
│   ├── lib/                        # cn(), readingPreferences, etc.
│   ├── router.tsx                  # getRouter() — QueryClient + router context
│   ├── telemetry.ts                # Browser OTel setup (VITE_OTEL_ENABLED)
│   └── styles.css                  # Tailwind entry — @theme tokens + shadcn + editorial layer
├── scripts/sync-dist.ts            # Copies dist/client → ../internal/staticfs/dist
├── components.json                 # shadcn/ui config (radix-mira, zinc)
├── vite.config.ts                  # tanstackStart({ spa: { enabled: true } }) + @tailwindcss/vite + dev proxy
├── tsconfig.json
├── bun.lock
└── package.json
```

Deferred scaffolding (not yet created): `deploy/helm/`,
`.github/workflows/release-please-config.json` ships, full CI lanes
spelled out in §9.

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

- **Handlers** — Gin `HandlerFunc`s. Four active surfaces:
  1. `/api/v1/*` — JSON. Auth + OIDC, me + account + identities,
     libraries, shelves, books (CRUD + cover + file + pages + progress
     + shelf membership + send-to-device), bookdrop (list / upload /
     cover / approve / reject / clear-processed), enrichment (search /
     stream / apply-match / locks / ISBN lookup / cover-from-url),
     stats (library + reading), annotations, devices, search,
     instance + config, admin settings (libraries / providers /
     metadata / oidc / users).
  2. `/opds/*` — Atom XML for e-readers, Basic-Auth-protected.
  3. `/events` — Server-Sent Events stream, cookie-authed.
  4. `/` (fall-through) — serves the embedded React SPA.

  Dependencies land in a `Deps` struct so `handler.New`'s signature
  stays flat as the app grows (~20 deps today; positional args would be
  unreadable). Responses are pure JSON on `/api/v1/*`, XML on OPDS,
  `text/event-stream` on `/events`.
- **Services** — Business logic. Plain Go structs wired with constructor
  functions in `cmd/embookshelf/main.go`. Current set:
  `AuthService`, `LibraryService` (+ `LibraryStore`), `ShelfService`,
  `BookDropService`, `ProgressService`, `EnrichmentService`,
  `AnnotationService`, `StatsService`, `ReadingSessionService`,
  `DeviceService` (+ `DeviceDriver` strategy), `OIDCService`,
  `SearchService`, plus internal-use helpers `MetadataWriter`
  (sidecar write-back), `LockMerger` (lock-aware metadata merge,
  `lock_merge.go`), `Placer` (write-target path resolver,
  `placer.go`), `ScanImport` (decision-driven side-effect runner,
  `scan_import.go` + `decide_effects.go`).
- **Repositories** — Hand-written SQL via pgx, one Postgres query text
  per statement (ADR-0023).
- **DTOs** — Request/response structs live alongside handlers
  (`userDTO`, `libraryDTO`, `bookDTO`, `bookDetailDTO`, `shelfDTO`,
  `bookdropDTO`, `enrichMatchDTO`, `deviceDTO`, `annotationDTO`,
  `identityDTO`, `oidcConfigDTO` + pointer-field PATCH types).
  camelCase on the wire; matching TS types under
  [ui/src/api/](../ui/src/api/).

### 4.2 Concurrency Model

- **Goroutines** handle every request; all I/O (DB, file, HTTP) is naturally non-blocking on the Go scheduler.
- **pgxpool** connection pool: 20 max connections, 5 min idle (tunable via env).
- `context.Context` is threaded through every call and respects request cancellation.
- **Caching** is intentionally absent today. Covers + static assets get HTTP
  `Cache-Control` headers; hot-path in-memory caching (ristretto) is a planned
  addition once a specific hot spot warrants it.

### 4.3 File Processing Pipeline (read + write)

Each supported format has a `fileproc.Processor` for **extract** and,
where applicable, an embedder for **write-back**. The interface
operates on a `storage.Source` (random-access reader) so the same
processor works for local files and S3 objects without copying bytes
through a temp file:

```go
type Metadata struct {
    Title, Author, Description, Language string
    HasCover                              bool
    CoverBytes                            []byte
    CoverMime                             string
    Format                                string
    DurationSeconds                       *int    // audio only
    Narrator                              string  // audio only
}

type Processor interface {
    Extract(ctx context.Context, src storage.Source) (Metadata, error)
}
```

`fileproc.Dispatch(path)` picks a processor by extension. Cover bytes
flow into `coverstore` (atomic write under `${DATA_PATH}/covers/` with
SHA-256 dedup since `000027`); presence + MIME live on
`books`/`bookdrop_items`.

| Processor | Status | Extract | Embed (write-back) |
|-----------|--------|---------|--------------------|
| `EPUBProcessor` | **Built** | stdlib `archive/zip` + `encoding/xml` | `epub_embed.go` rewrites cover + OPF metadata |
| `PDFProcessor` | **Built** | `pdfcpu` info pull | `pdf_embed.go` writes Info dictionary updates |
| `CBZProcessor` | **Built** | `archive/zip` page count + first-image cover | — (read-only format) |
| `AudioProcessor` | **Built** | `dhowden/tag` for MP3/M4A/M4B (title, artist, narrator, duration) | — (deferred) |
| CBR | Deferred | needs `nwaples/rardecode` | — |
| AZW3 / MOBI / FB2 | Deferred | parser TBD | — |

Unsupported files dropped today are queued, surface
`ErrUnsupportedFormat` in the review UI, and can still be approved
manually — they just carry no extracted metadata.

#### Write-back: sidecars + in-file embed (ADR-0001)

Edit-side metadata changes do not silently mutate the book file alone.
The pipeline is symmetric with extraction:

1. **`internal/sidecar/`** writes `<basename>.embookshelf.json`
   (native, paired filename, lossless) and a Calibre-compatible
   `metadata.opf` next to the book bytes. The JSON sidecar is the
   round-trip canonical: `Sidecar = model.EditableMetadata`.
2. **`fileproc/*_embed.go`** writes the same fields into the book
   file itself (EPUB OPF + cover, PDF Info dictionary). Embedding is
   best-effort; failures fall back to sidecar-only without rolling
   back the DB write.
3. **Reattach on rescan** — `internal/scan/reattach.go` reads the
   sidecar before the differ decides whether a rediscovered file is
   the same logical book, so user edits survive `library.scan` even
   if the file was renamed or moved.

Field-level locks (`book_metadata_locks`, migrations `000020`)
prevent enrichment from overwriting fields the user has explicitly
pinned. `service.MergeLocked` enforces the lock set when merging
provider matches into the persisted record.

### 4.4 Async Task System

Background work runs through `queue.Client`, backed by River
(`riverqueue/river` over `riverpgxv5`). Schema applied at boot via
`rivermigrate`. 4 max workers on the default queue. Horizontal scale +
River's own dashboard come along. Crash recovery is River's JobRescuer,
which reclaims jobs left `running` by a killed process.

`Client` is deliberately one method wide — `Enqueue(ctx, args)` plus
`Stop` — because the kind travels with the payload, so adding a job type
does not widen the interface. `queue/registry.go` declares each job once
(kind + args type + work function) and derives River's typed-worker
registration from it; `internal/task/` never imports river.

`internal/task/` contains the per-kind workers:

- `BookDropWorker` (`bookdrop.ingest`) — runs the `fileproc` pipeline,
  stores the cover, transitions the queue row.
- `LibraryScanWorker` (`library.scan`) — walks a library root via
  `internal/scan` (walker + classifier + differ), groups files into
  classified `LeafBook`s (ADR-0003), then enqueues one
  `scan.import` job per leaf.
- `ScanImportWorker` (`scan.import`) — runs `service.ScanImport` to
  decide effects (create / update / reattach) and apply them via a
  shared decision-driven runner (`decide_effects.go`).

S3-backed installs use the orphan sweeper to delete keys left behind
by edit-time folder renames after a grace window (ADR-0005,
`pending_orphans` table, env `EMBOOKSHELF_S3_RENAME_GRACE`).

The **SSE hub** (`internal/sse/`) broadcasts per-item updates.
`BookDropService` calls `hub.Broadcast` on every state transition.
The [`/events` handler](../internal/handler/events.go) subscribes
each connected browser to the hub, sends a 25-second heartbeat
(`: ping`) to defeat idle proxy timeouts, and tears down on client
disconnect. The React side (`useRealtime`) opens a single
`EventSource` inside the authed layout and dispatches each named
event into react-query cache invalidations — see §5.7.

Job progress lives on the domain row itself
(`bookdrop_items.state` / `progress` / `error_msg`); a generic
`task_history` table is deferred until more job kinds warrant
unification.

### 4.5 Authentication Flow

```
                    ┌──────────────┐
                    │   Browser    │
                    │ (cookie jar) │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
       ┌──────┴───┐ ┌─────┴────┐ ┌─────┴──────┐
       │ Session  │ │   OIDC   │ │  Remote    │
       │ cookie   │ │  (built, │ │  Auth      │
       │ (local)  │ │  multi)  │ │ (planned)  │
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
                  │ auth.UserFrom-  │
                  │ Context(ctx)    │
                  └─────────────────┘
```

- **Local session auth** *(built)* — `POST /api/v1/auth/login` verifies
  the password (bcrypt) and inserts a row into `sessions`. The session
  id (random UUID) rides in an `HttpOnly; SameSite=Lax` cookie. Every
  `/api/v1/*` request gated by `auth.RequireAuth` runs a single atomic
  `UPDATE sessions SET last_used_at = now() WHERE id=$1 AND expires_at > now() RETURNING ...`
  — sliding-window session in one query. 7-day TTL, slid forward on
  every request. `POST /api/v1/auth/logout` destroys the row + clears
  the cookie. `GET /api/v1/me` returns the current user for the SPA's
  auth gate. Expired sessions are purged opportunistically at boot.
- **Basic Auth for OPDS** *(built)* — `/opds/*` uses HTTP Basic via
  `auth.BasicAuth`. E-reader apps don't maintain session cookies, so
  credentials are verified against the `users` table per request
  (`AuthService.Verify` — no session created).
- **First-run bootstrap** *(built)* — `POST /api/v1/auth/signup` calls
  `AuthService.Signup`, which refuses once `users` is non-empty. The
  first account becomes admin. The SPA's `/login` route checks
  `GET /api/v1/auth/signup` to decide whether to show the signup form.
- **CSRF** — `auth.CSRFGuard` middleware runs globally on every
  non-safe request, asserting `Origin`/`Referer` matches `Host`. Paired
  with `SameSite=Lax` cookies this is sufficient for a same-origin SPA
  without per-form tokens. Cross-origin dev is handled via the Vite
  proxy, which rewrites Origin so the check still passes.
- **OIDC / SSO** *(built)* — `coreos/go-oidc` discovery +
  `golang.org/x/oauth2` authorization-code flow. Per-provider slugs
  (`google`, `github`, `generic`) with one shared callback at
  `/api/v1/auth/oidc/callback`; the `state` token carries the slug so
  the service knows which provider issued it. Configuration is
  **DB-backed**, not env: admins edit issuer/client/secret/scopes via
  `/api/v1/settings/oidc` (AES-GCM-encrypted at rest per ADR-0010),
  and `service.OIDCService` reloads after each write. Multi-provider
  identity linking goes through the `user_identities` table
  (ADR-0007); a single user can link Google + GitHub + generic at
  once. Account-side flows live under `/api/v1/account/oidc/*`
  (link / unlink / set initial password for OIDC-provisioned users
  who never had one). New-user provisioning honors the
  `user_approval_status` workflow (`000023_user_approval_status`):
  admins approve or deny first-login OIDC users before they can sign
  in fully.
- **Remote/Forward Auth** *(planned)* — middleware that trusts
  `Remote-User` / `Remote-Email` / `Remote-Groups` reverse-proxy
  headers. Not wired today.
- **JWT** *(not planned for the web SPA)* — server-side sessions keep
  revocation free and play well with SameSite cookies. A JWT
  access-token layer can still be added to `/api/v1/*` later for
  external API clients without touching the SPA auth.

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

### 4.7 Storage Backends

Books and sidecars never touch the DB layer directly — every read,
write, list, copy, delete, and random-access open goes through the
`storage.Storage` interface in `internal/storage/`. Two concrete
backends ship today, with a third (capability-gated) shape for any
future driver.

| Backend | Package | Notes |
|---------|---------|-------|
| Local POSIX FS | `internal/storage/local` | Default for self-hosted single-machine installs. `Copy` uses `rename(2)` on same-FS, falls back to copy + unlink. |
| AWS S3 (and S3-compatible: MinIO, R2, B2) | `internal/storage/s3` | AWS SDK v2; presign + server-side copy; iterator wraps `ListObjectsV2`. Boot-time check warns when bucket versioning is disabled (does not refuse to start). |

Per-library mapping. A library row holds `kind` (local|s3) +
`backend_id` + `root` (`storage_v2`, migration `000025`). At boot
`storageloader.LoadStorageBackends` reads `storage_backends` and
returns a `storage.Resolver` so each request can route to the right
backend by library. The first row also serves as the default for
legacy single-library installs that predate `storage_v2`.

**Capabilities** are advertised via a bitset (`CapPresign`,
`CapStorageClass`, `CapVersioning`, `CapNotify`, `CapConditional`,
`CapRange`). Code paths that want presigned URLs or `If-Match`
preconditions gate on `Capabilities() & Cap*`; backends without the
capability return `ErrUnsupportedOption`.

**Book file delivery.** Two modes:

- **Stream** (default, `EMBOOKSHELF_PRESIGN_FALLBACK=""` or
  `"stream"`) — server reads from `Storage.Get` and pipes bytes to
  the client. Always works. Bytes traverse the app server.
- **Presign** (`EMBOOKSHELF_PRESIGN_FALLBACK="presign"`) — server
  issues a 302 redirect to a pre-signed URL with TTL
  `EMBOOKSHELF_PRESIGN_TTL` (default 10m). Saves bandwidth and
  app-server CPU. **Requires the bucket to allow the SPA origin via
  CORS** — epub.js / pdf.js XHRs follow the redirect cross-origin
  and will fail without CORS.

**S3 edit-time folder rename (ADR-0005).** Renaming a book's folder
on S3 is `Copy` + deferred `Delete`. The old keys land in
`pending_orphans` (migration `000032`) with `eligible_at = now() +
EMBOOKSHELF_S3_RENAME_GRACE` (default `max(2 × PresignTTL, 1h)`); a
sweeper deletes after the window so already-issued presigned URLs
don't 404 mid-download.

### 4.8 Search

A single `/api/v1/search` endpoint backs the global command palette
(`CommandPalette.tsx`, ⌘K) and the library page combobox
(`LibrarySearchCombobox.tsx`); the OPDS `/opds/search` route reuses
the same query layer. Cross-entity: results include books, shelves,
libraries, and authors.

One engine: **`tsvector` + GIN.** `books` carries a `tsv` generated
column (title || author || description) with a GIN index. Queries use
`websearch_to_tsquery` so the user-facing query string is Postgres'
battle-tested mini-syntax (quoted phrases, `or`, `-`).

`SearchService` does the cross-entity fan-out and merge; ranking is
`ts_rank`.

---

## 5. UI Architecture

### 5.1 TanStack Start in SPA mode

The UI is a [TanStack Start](https://tanstack.com/start) app configured
in **SPA mode** — no SSR, no Node runtime in production. Why Start over
vanilla `@tanstack/react-router`? File-based routing, generated typed
routes, `createFileRoute` with loaders and `validateSearch`, and a clear
upgrade path if we ever need partial prerendering. SPA mode is opted in
via the Vite plugin:

```ts
// ui/vite.config.ts
tanstackStart({ spa: { enabled: true } })
```

Start still runs a prerender pass during build to emit `_shell.html`
(the static entry point); the sync script duplicates that file as
`index.html` so Go's SPA fallback finds it.

The route tree is regenerated on every dev change and build —
`ui/src/routeTree.gen.ts` is git-ignored. TypeScript module augmentation
in [`ui/src/router.tsx`](../ui/src/router.tsx) teaches the type system
the shape of our router so `<Link to>`, `useParams`, `useSearch`, and
`navigate` are all typed end-to-end.

### 5.2 Route tree

Routes are file-based. A leading underscore makes a **pathless layout
parent** whose children inherit the layout but not a URL segment; dots
stand in for nested folders.

| File | URL | Component |
|------|-----|-----------|
| `__root.tsx` | — | html/body shell, HeadContent, Scripts, QueryClientProvider, sonner `Toaster` |
| `_app.tsx` | — | Sidebar + `<main>` + status bar layout, `<Outlet />` in the middle. `beforeLoad` enforces auth + approval status. |
| `_app.index.tsx` | `/` | Dashboard (currently reading, 12-week heatmap, stats, libraries) |
| `_app.library.tsx` | `/library` | LibraryView — shelf/grid/list, filter, sort; `?shelf=`, `?layout=` in search |
| `_app.book.$id.tsx` | `/book/:id` | BookDetail — overview / notes / annotations / versions / activity tabs |
| `_app.book.$id_.edit.tsx` | `/book/:id/edit` | MetadataEditor + EnrichmentPanel + field-lock toggles |
| `_app.book.$id_.find.tsx` | `/book/:id/find` | Provider-match search workspace (`enrich/stream` SSE) |
| `_app.notebook.tsx` | `/notebook` | Cross-book notes + highlights |
| `_app.bookdrop.tsx` | `/bookdrop` | Import review queue (list + detail split + drag-and-drop upload) |
| `_app.stats.tsx` | `/stats` | Library totals, reading activity heatmap, bar charts |
| `_app.account.tsx` | `/account` | Account hub — profile, password, linked OIDC identities |
| `_app.settings.tsx` | `/settings` | Admin hub — instance, libraries, providers, metadata, OIDC, users |
| `read.$id.tsx` | `/read/:id` | Full-screen Reader — intentionally outside `_app` so the sidebar is hidden. Dispatches to Epub/Pdf/Comic/Audio by `book.format`. |
| `login.tsx` | `/login` | Local session login + first-run signup + OIDC entrypoints |
| `login.pending.tsx` | `/login/pending` | Awaiting-admin-approval landing for first-login OIDC users (ADR-0007) |

The Sidebar uses `useRouterState` to determine the active pathname +
search params so a shelf/library click navigates via `<Link to="/library" search={{ shelf: 'reading' }}>`
rather than mutating local state. That's the big behavioral shift from
the prototype's single-component `setView` state machine.

### 5.3 Data flow

- **Server state** — `@tanstack/react-query`, one `QueryClient`
  instance created inside `getRouter()` and injected via the router
  context so a loader can call `queryClient.ensureQueryData()` if we
  ever want route-level prefetching. Default `staleTime: 30_000`,
  `refetchOnWindowFocus: false`.
- **Fetch wrapper** — [`ui/src/api/client.ts`](../ui/src/api/client.ts).
  Always `credentials: 'include'` (the session cookie must ride along),
  JSON-body detection, uniform error shape `{ status, message }`. Empty
  bodies on 202/204/205 are handled (some fire-and-forget endpoints
  return no content).
- **Search params** — routes that care about URL state declare a
  `validateSearch` zod-free parser so the types are tight without an
  extra runtime dep (see
  [`_app.library.tsx`](../ui/src/routes/_app.library.tsx)).
- **Typed API modules** — per-resource clients live under
  [ui/src/api/](../ui/src/api/) (`auth`, `books`, `bookdrop`, `devices`,
  `annotations`, `enrich`, `settings`, `stats`, `reading`, `realtime`).
  Each exports query keys + fetchers so `useQuery`/`useMutation` call
  sites stay one-liner.
- **Mock data** — [`ui/src/data/mock.ts`](../ui/src/data/mock.ts) is
  the reference dataset from the original prototype. Live routes are
  now backed by the real API; mock exports remain as the source of
  truth for types + the non-authenticated palette/style vocabulary.
- **Auth state** — `useQuery({ queryKey: meQueryKey })` against
  `/api/v1/me`. The `/_app` layout's `beforeLoad` calls
  `queryClient.ensureQueryData` on that key and redirects to `/login`
  when `null`; login/logout mutate the cache directly and navigate.

### 5.4 Component library

Components are grouped by role. shadcn primitives + editorial chrome
live at the top level; feature subtrees own their own primitives:

```
ui/src/components/
├── ui/                      # shadcn/ui primitives (button, input, dialog, …)
├── account/                 # Account hub panels (identities, password, profile)
├── settings/                # Admin settings panels (libraries, providers, metadata, OIDC, users)
├── metadata/                # MetadataEditor + EnrichmentPanel + LockToggle + FieldRow
├── EpubReader.tsx PdfReader.tsx ComicReader.tsx AudioReader.tsx
├── Cover.tsx Sidebar.tsx TopBar.tsx Icon.tsx
├── RuleEditor.tsx ShelfCreatorDialog.tsx ShelfDraftProvider.tsx AccentPicker.tsx
├── CommandPalette.tsx LibrarySearchCombobox.tsx
└── SettingsShared.tsx
```

Two layers sit side-by-side in [`ui/src/components/`](../ui/src/components/):

**shadcn/ui primitives** under
[`components/ui/`](../ui/src/components/ui/) — installed via
`bunx shadcn@latest add ...` with the **radix-mira** style + `zinc`
base color (see [`ui/components.json`](../ui/components.json)). Each
primitive is a thin wrapper around [radix-ui](https://www.radix-ui.com)
or a native element with CVA-driven variants:

| Primitive | Used by |
|-----------|---------|
| `Button`, `Input`, `Label`, `Textarea` | Login, settings forms, BookDrop approval, metadata editor |
| `Dialog`, `DialogContent`, `DialogTitle` | `RuleEditor` (smart-shelf create/edit) |
| `Select`, `SelectTrigger`, `SelectContent`, `SelectItem` | Settings (reading prefs, role picker), RuleEditor predicates |
| `Switch` | Settings (reading prefs, feature toggles) |
| `Checkbox`, `Slider`, `Progress` | Reader controls, stats charts |
| `Tabs`, `TabsList`, `TabsTrigger`, `TabsContent` | BookDetail (overview / notes / annotations / versions / activity) |
| `DropdownMenu`, `Popover`, `Tooltip` | Send-to-device, overflow menus |
| `Card`, `Separator`, `Badge`, `ScrollArea` | Settings panels, stat tiles |
| `Sonner` (`Toaster`) | Mounted in `__root.tsx`; driven by `toast()` at mutation sites |

**Editorial components** (bespoke, on top of the custom `@theme`
tokens + `.chip` / `.cover` / `.t-*` utility layer defined in
[`ui/src/styles.css`](../ui/src/styles.css)):

- [`Icon.tsx`](../ui/src/components/Icon.tsx) — 46 hand-drawn icons
  from the original prototype, typed `IconName` union, stroke 1.5. Kept
  separate from `lucide-react` so the editorial voice of the book
  chrome stays distinct from shadcn's default icon set.
- [`Cover.tsx`](../ui/src/components/Cover.tsx) — `Cover`, `Spine`,
  `StarRating`. `Cover` takes `{ book, size, onClick, style }`;
  switches on `book.style` for 5 typographic cover styles, on
  `book.palette` for 10 bookcloth-inspired colors, and on `size` for
  xs/sm/md/lg/hero. Placeholder books render as diagonal-stripe paper
  tile.
- [`Sidebar.tsx`](../ui/src/components/Sidebar.tsx) — router-aware
  navigation with library/shelf/smart-shelf sections, hover-reveal
  edit/delete affordances, and the `UserBadge` footer.
- [`TopBar.tsx`](../ui/src/components/TopBar.tsx) — sticky header with
  title + subtitle + crumbs + search + right slot, reused across most
  in-app views.
- [`RuleEditor.tsx`](../ui/src/components/RuleEditor.tsx) —
  predicate-builder dialog for smart shelves. Wrapped in a shadcn
  `Dialog` with `Select`/`Input` predicate rows and editorial `Button`
  variants.
- [`EpubReader.tsx`](../ui/src/components/EpubReader.tsx) +
  [`PdfReader.tsx`](../ui/src/components/PdfReader.tsx) — imperative
  reader surfaces; see §5.6.

The split is intentional: shadcn carries the common-case form/menu
surface so accessibility + dark mode + focus rings are free, while the
custom editorial classes own the "built like a printed book" chrome
(warm ivory paper tones, Source Serif 4, oklch cover palette, cloth
bindings) that no shadcn preset ships with.

### 5.5 Styling: Tailwind 4 + shadcn/ui

Tailwind 4's **CSS-first configuration** drives the design system.
Tailwind now ships via the first-class [`@tailwindcss/vite`](https://tailwindcss.com/blog/tailwindcss-v4)
plugin — no standalone CLI watcher, no generated stylesheet side-car.

[`ui/src/styles.css`](../ui/src/styles.css) holds the Google Fonts
import, Tailwind + shadcn bases, `@theme` tokens, the `:root` / `.dark`
shadcn color scales, and a `@layer components` block for the editorial
primitives (chips, covers, shelf plank, status bar, typography scale):

```css
@import url('https://fonts.googleapis.com/css2?family=Source+Serif+4...');
@import "tailwindcss";
@import "tw-animate-css";
@import "shadcn/tailwind.css";
@import "@fontsource-variable/inter";

@custom-variant dark (&:is(.dark *));

@theme inline {
  /* shadcn + radix-mira tokens */
  --color-primary:  var(--primary);
  --color-card:     var(--card);
  --radius-md:      calc(var(--radius) * 0.8);
  ...

  /* Editorial overlay — warm ivory, navy-tinted ink, library burgundy */
  --color-paper-0:  oklch(0.985 0.006 85);
  --color-ink-1:    oklch(0.20 0.018 255);
  --color-editorial-accent: oklch(0.40 0.095 25);

  /* Cover palette — 10 muted bookcloth colors (NYRB / Penguin Classics vibe) */
  --color-cov-olive:  oklch(0.45 0.055 115);
  --color-cov-teal:   oklch(0.38 0.048 210);
  ...

  /* Typography */
  --font-serif:  "Source Serif 4", Georgia, serif;   /* UI + display */
  --font-reader: "Literata",       Georgia, serif;   /* long-form reading */
  --font-mono:   "IBM Plex Mono",  ui-monospace, monospace;
}

:root { --primary: oklch(0.555 0.163 48.998); --radius: 0.45rem; ... }
.dark { --primary: oklch(0.473 0.137 46.201); ... }

@layer components {
  .chip, .cover, .cov-*, .shelf-plank, .progress, .status-bar,
  .fade-in, .t-h1, .t-h2, .t-label, ...
}
```

Two sets of design tokens coexist:

1. **shadcn / radix-mira tokens** — `--primary`, `--card`,
   `--muted-foreground`, `--ring`, `--radius`, etc. These drive every
   primitive under `components/ui/` and support light / dark variants
   via the `.dark` class.
2. **Editorial tokens** — `--color-paper-*`, `--color-ink-*`,
   `--color-cov-*`, `--color-editorial-accent`, `--font-serif`,
   `--font-reader`, `--font-mono`. These are consumed by the custom
   `Cover`, `Sidebar`, `TopBar`, and the `.chip` / `.cover` /
   typography utility classes.

Typography stack:

- **Source Serif 4** (Adobe, variable `opsz 8..60`) for UI + display.
- **Literata** (commissioned by Google for Play Books) for the reader
  body via `--font-reader`.
- **IBM Plex Mono** for metadata chrome.
- **Inter Variable** (via `@fontsource-variable/inter`) is available
  as `--font-sans` for shadcn surfaces that default to sans.

Fonts are pulled from Google Fonts today; self-hosting them under
`internal/staticfs/dist/fonts/` is a planned follow-up to drop the
network dependency on first paint.

### 5.6 Built-in readers

| Reader | Status | Notes |
|--------|--------|-------|
| EPUB | **Built** | [`EpubReader`](../ui/src/components/EpubReader.tsx) wraps epub.js with an imperative handle (`next` / `prev` / `goTo`). Paginated flow, `book.locations.generate(1024)` powers the percentage scrubber, `relocated` event reports `{percent, cfi}`. Typography overrides via `rendition.themes.default` so font/size changes survive chapter transitions. TOC tree flattened for the Contents panel. |
| PDF | **Built** | [`PdfReader`](../ui/src/components/PdfReader.tsx) uses pdfjs-dist 5. Worker URL resolved via `new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url)` so Vite emits a hashed worker. Scroll container with one `<canvas>` per page; only current ± 1 page are rasterized (others cleared) to keep memory flat on large PDFs. |
| Comic / CBZ | **Built** | [`ComicReader`](../ui/src/components/ComicReader.tsx) reads per-page images from `/api/v1/books/:id/pages/:n` (server expands the archive once, caches page bytes). Keyboard nav + double-page spread + manga mode. CBR + cb7 deferred. |
| Audiobook (MP3/M4B) | **Built** | [`AudioReader`](../ui/src/components/AudioReader.tsx) over the native `<audio>` element. Server delivers bytes via the same `/files` path (range requests). Duration + narrator pulled by `dhowden/tag` at ingest. Chapter markers from M4B containers TBD. |

#### 5.6.1 Progress tokens

One column (`user_book_progress.resume_cfi`) carries every reader's
resume marker; format is prefix-discriminated:

| Format | Token | Example |
|--------|-------|---------|
| EPUB | `epubcfi(...)` | `epubcfi(/6/14[chap03]!/4/1:0)` |
| PDF | `page:N` | `page:42` |
| Comic / CBZ | `page:N` (0-indexed) | `page:7` |
| Audiobook | `time:Ns` | `time:3600s` |

[`read.$id.tsx`](../ui/src/routes/read.$id.tsx) dispatches to the
right component by `book.format`, debounces progress writes by
600 ms, and flushes any pending tick via a raw `fetch` inside the
cleanup effect so a short reading session still records progress.

Reader typography (font, size, line-height) is driven per-user by
[`readingPreferences.ts`](../ui/src/lib/readingPreferences.ts) — stored
in `localStorage`, editable from the Settings → Reading preferences
pane via shadcn `Select` + `Switch` + range inputs. Promoting this to a
server-side `reader_preferences` table is the next step for cross-device
parity.

### 5.7 Realtime (SSE)

[`useRealtime`](../ui/src/api/realtime.ts) is mounted once inside the
authed layout (`_app.tsx`). It opens a single
`EventSource('/events', { withCredentials: true })` per session — the
browser reuses the same cookie that the JSON API calls carry, so no
separate handshake is needed.

Event → cache-invalidation dispatch is a typed `Record<RealtimeEvent,
Handler>` map; TypeScript enforces exhaustive coverage, so adding a
new event name surfaces missing handlers at build time. Today
`bookdrop.updated` invalidates the bookdrop queue + books list +
libraries (for book-count updates after approvals), and
`bookdrop.cleared` re-fetches just the queue after the user empties
the "Recently processed" section.

The native `EventSource` handles reconnection + exponential backoff
automatically; the hook only wires teardown (`removeEventListener` +
`es.close()`) on unmount. A future slice could add a connection-status
indicator for cases where the reverse proxy severs the stream with no
retry budget left.

One connection per session means the subscribing effect must run once,
so nothing that changes during a session may enter its dependency
array. The two shared-shelf handlers do need the current route — they
redirect a viewer sitting on a shelf that was just un-published — and
they read it at *event* time from `useRouter().state.location`, a live
getter on a router instance whose identity never changes. Subscribing
to the location with `useRouterState` instead would put a
navigation-scoped value in the deps and reopen the stream on every
route change.

---

## 6. Data Model

### 6.1 Core Domain Entities

Schema is at migration `000032`. Paired up/down SQL files live under
`internal/migrator/migrations/postgres/`.

The catalog backbone:

```
storage_backends ──┬── (kind: local | s3, config jsonb)
                   │
libraries ─────────┼── (backend_id, root, slug, kind, name, accent)
                   │
                   └── files ──┬── (location, size, etag, content_hash,
                               │     format, mtime, last_scanned)
                               │
                               └── books ──┬── (title, author, format, year,
                                          │    rating, publisher, series,
                                          │    series_index, tags, isbn,
                                          │    description, language, cover_palette,
                                          │    has_cover, cover_mime, cover_hash,
                                          │    folder_path, uuid, tsv, deleted_at)
                                          ├── book_metadata_locks (per-field lock set)
                                          ├── audiobook_metadata (duration, narrator)
                                          └── user_book_progress (user_id, progress,
                                               resume_cfi, last_read_at)

users ──┬── sessions (id, expires_at, user_agent, last_used_at)
        ├── user_identities (provider, issuer, subject — ADR-0007)
        └── shelves ── shelf_books          (smart shelves carry a rule blob)
```

Other tables, grouped by role:

- **Ingest.** `bookdrop_items` (state machine: discovered → extracting
  → ready → approved/rejected), `pending_orphans` (S3 rename grace
  buffer, ADR-0005).
- **Reading.** `annotations` (highlights, notes, bookmarks),
  `reading_sessions` (time-spent rollups for `/stats/reading`).
- **Devices.** `user_devices` (per-user push destinations,
  ADR'd shape; `secret` never leaves the server).
- **Providers.** `provider_settings` (enabled flag),
  `provider_settings_config` (AES-GCM-encrypted blobs, ADR-0010),
  `provider_health` (last success/failure, used by §7.1.4).
- **System.** `app_settings` (singleton k/v config — instance name,
  signup-open flag, default library), `oidc_settings`
  (DB-backed OIDC config replacing env vars).
- **Queue.** River's `river_*` tables, created by `rivermigrate` on
  boot.

Removed since the previous edition: file-naming pattern config
(`000028`, ADR rationale: book-per-folder makes patterns redundant)
and `libraries.org_mode` (`000030`, only one mode survived).

Genuinely future tables (no migration yet):

- `audit_logs`, `user_content_restrictions` (parental gates).
- `reader_preferences` (server-side promotion of today's `localStorage`
  preferences for cross-device parity).
- `email_providers` (Send-to-Kindle blocked on SMTP).

Postgres features used across the schema:

- `jsonb` for flexible config payloads.
- `tsvector` + GIN indexes for search — see §4.8.
- `text[]` for tag lists and other repeated scalars.
- Partial indexes (`WHERE deleted_at IS NULL`) for soft-deleted rows.
- `uuid` primary keys. Repos generate ids in Go via `db.NewID()` rather
  than leaning on the `gen_random_uuid()` default, so the caller knows
  the id without a `RETURNING` round-trip.

### 6.2 Database Management

- **golang-migrate/migrate** manages schema evolution. Paired up/down
  SQL files live under `internal/migrator/migrations/postgres/`.
  Driver: `pgx/v5`.
- Migrations are idempotent where practical (`CREATE ... IF NOT EXISTS`,
  `ADD COLUMN IF NOT EXISTS`).
- Released migrations are never modified; new migrations are created for changes.
- The app embeds `migrations/` (`//go:embed all:migrations`) and runs the
  Postgres tree on boot by default. Opt out with `MIGRATE_ON_START=false`
  if migrations are managed externally via `go run ./cmd/migrate up`.
- River's own schema migrations are applied separately by
  `rivermigrate` inside `queue.New`.
- A second tree, `internal/migrator/migrations/sqlite/`, is embedded but
  never served. It exists so `embookshelf import-sqlite` can bring an
  old source database forward to the current schema before reading it —
  an operator may upgrade from any old release in one hop. CI job
  `migrations-sanity-sqlite-importer` keeps it honest. Both the tree and
  the `modernc.org/sqlite` driver go when the importer is retired
  (ADR-0023).

#### Migrating an existing SQLite install

```
DATABASE_URL='postgres://…/embookshelf' \
  embookshelf import-sqlite --from ./data/embookshelf.db
```

`internal/sqliteimport` copies tables in foreign-key-safe order,
asking Postgres for each column's type and converting the two encodings
that genuinely differ: JSON-text arrays become `text[]`, RFC3339 TEXT
timestamps become `timestamptz`. Notable behaviour:

- **The target must be empty.** Importing onto a populated database
  would interleave two libraries, so it refuses instead of merging.
  Migrations are applied to the target automatically.
- **Orphan rows are skipped and reported.** SQLite runs with
  `PRAGMA foreign_keys` off by default, so a long-lived source database
  can hold rows whose parent no longer exists — Postgres rejects them.
- **Queued jobs do not transfer.** The old `jobs` table has no River
  equivalent; pending work must be re-triggered after the import.

---

## 7. External Integrations

### 7.1 Metadata Providers

| Provider | Status | Auth | Usage |
|----------|--------|------|-------|
| Google Books API | **Built** | Anonymous (low-volume) | Title, author, description, categories, cover, ISBN, publisher, year |
| Open Library API | **Built** | Anonymous | Title, author, description, cover, ISBN, first-publish-year |
| Hardcover | **Built** | Bearer API key | Reading-status + rich metadata + cover |
| Goodreads | **Built** | Cookie (session scrape) | Cover + metadata fallback (deprecated public API replaced by HTML-scrape path) |
| Amazon | **Built** | Anonymous (HTML scrape) | Cover-image + ISBN/title fallback |
| DuckDuckGo | **Built** | Anonymous | Cover-image search fallback |

All implement `provider.Provider`. Built via `provider.Build(name)`,
which looks up rate-limit and metadata in the **provider catalog**
(see §7.1.3) and wraps the underlying client in a
`ResilientClient` (rate-limit token bucket + backoff).

Cover-fetch from a provider URL goes through
`EnrichmentService.ImportCoverFromURL`, which **hard-enforces an
allow-list of hosts** (Google Books, Open Library, Amazon image
CDN, Goodreads/Hardcover assets), rejects non-HTTPS, rejects
non-`image/*` Content-Type, and caps body size at 10 MB — SSRF
protection baked in.

#### 7.1.1 Fan-out + graceful degrade (ADR-0013)

`EnrichmentService.Search` runs every enabled provider concurrently
via `errgroup.WithContext`. **A failing provider is logged but does
not cancel its peers** — the user gets the partial result rather
than a hard error. Results are merged and sorted by a confidence
heuristic (`provider/score.go`).

The streaming variant `POST /api/v1/books/:id/enrich/stream`
returns SSE frames as each provider completes (`match`,
`provider-error`, `done`); client disconnect cancels in-flight HTTP
via context propagation, so closing the editor stops every
provider's outbound request.

Auto-applying the top match is deliberately avoided. The user
always confirms — except for **auto-enrich-empty-only**
(ADR-0012): if a book row has no metadata at all (only a filename),
the highest-confidence match is applied automatically on first
ingest.

#### 7.1.2 ISBN priority chain (ADR-0011)

`POST /api/v1/books/metadata/isbn-lookup` does not just fan out. It
walks providers in a **fixed priority order** and **returns the
first successful match** rather than merging:

1. Hardcover (richest structured data when keyed)
2. Google Books
3. Open Library
4. Amazon (cover + minimal metadata)
5. Goodreads / DuckDuckGo (last-resort fallbacks)

The chain stops on the first non-empty result — different from the
free-text search where every provider runs. ISBN is canonical, so
the first authoritative answer wins.

#### 7.1.3 Provider catalog in binary (ADR-0008)

`internal/provider/catalog.go` is the static registry of provider
names + display labels + rate-limit defaults + config schemas. Lives
in the binary, not the database. This way: adding a provider is one
catalog entry + one `Build` switch arm; rate limits don't need a
migration; admins can't accidentally enable something the binary
doesn't ship a driver for.

`provider_settings` (DB) only stores the per-instance overrides:
enabled bool, encrypted config blob.

Provider configs are AES-256-GCM encrypted at rest (ADR-0010) under
`EMBOOKSHELF_SECRET_KEY`. Cookie + API-key fields use the catalog's
`ConfigField` schema so the admin UI renders the right input
(`text` / `password` / `select` / `textarea`); secrets are sent to
the server as plaintext and encrypted before insert. Plaintext
storage is allowed in dev (no key set) but logs a warning at boot.

#### 7.1.4 Health + admin surface

`provider_health` records last-success / last-failure / last-error
per provider; the Settings → Providers panel surfaces the badge so
admins know when a credential has rotted before users start
reporting empty matches. The same panel writes the schema-driven
config form, hits `provider.Configure(raw)`, and persists the
encrypted blob.

### 7.2 Device Sync Protocols

Two integration patterns coexist: **pull-based** (the reader asks the
server for a catalog and fetches files itself) and **push-based** (the
server sends a book to a paired device through the vendor's cloud).

| Protocol/Device | Status | Integration Pattern |
|-----------------|--------|---------------------|
| OPDS 1.2 | **Built** (pull) | Atom/XML served at `/opds/*` with HTTP Basic Auth. Root nav + All / Library / Recent / Search acquisition feeds + OpenSearch description + per-book download/cover. Works with KOReader, Moon+ Reader, FBReader, Aldiko, Marvin, etc. |
| reMarkable Paper Pro (RM2/RM1/Paper Pro) | **Built** (push) | Cloud-push via `webapp-prod.cloud.remarkable.engineering` (pair) + `internal.cloud.remarkable.com/doc/v2/files` (upload). Users pair once with the 8-char one-time code from `my.remarkable.com/device/desktop/connect`; the server stores the long-lived device token and mints short-lived user tokens per push. EPUB + PDF only. Driver: `internal/service/device_remarkable.go`. |
| Kobo | Deferred | REST compatibility layer emulating Kobo's cloud endpoints. Deferred because the protocol is undocumented + proprietary; emulating it well is a multi-week reverse-engineering effort per-device. |
| KOReader | Deferred | REST sync API for reading progress |
| Hardcover.app | Deferred | REST API integration for reading-status sync |
| Komga | Deferred | REST API for comic-library import |
| Kindle (Send-to-Kindle) | Deferred | Uses email delivery (§Email); blocked on SMTP transport. |

#### 7.2.1 Push-to-device architecture

The push path is driver-pluggable. One Go interface covers every future
device kind:

```go
type DeviceDriver interface {
    Kind() model.DeviceKind
    Pair(ctx, params) (model.Device, error)
    Send(ctx, device, content, meta) error
}
```

- **Storage.** `user_devices` table (migration `000013_devices`) holds
  one row per registered destination: `user_id`, `kind`, `name`,
  `secret` (pairing token — never exposed over the API), `config`
  (JSONB, per-driver knobs), and `last_sent_at` / `last_error` for UI
  status.
- **Service.** `service.DeviceService` indexes drivers by `Kind` and
  mediates pairing + sending. Each successful push updates
  `last_sent_at`; failures are recorded on the same row so the Settings
  panel can surface "last error" without querying logs.
- **Handlers.** `GET/POST /api/v1/devices`, `DELETE /api/v1/devices/:id`,
  `POST /api/v1/books/:id/send/:deviceId`. All cookie-authed and
  per-user — a device registered by user A is invisible to user B.
- **UI.** Settings → Device sync lists registered devices and pairs new
  ones via a modal form. The book detail page has a "Send to device"
  dropdown; when no device is paired, the button deep-links into
  Settings.

Adding a new kind (Kindle, Boox, …) is one file plus a line in
`main.go` registering the driver.

---

## 8. API Design

Three surface areas exist side by side:

- **JSON API** — `/api/v1/*`. Auth, library / shelf / book CRUD +
  cover + file streaming + progress, bookdrop, enrichment, admin
  settings. Cookie-authed via `auth.RequireAuth` except the auth
  subtree and healthcheck. Admin-only routes stack `auth.RequireRole`.
- **OPDS catalog** — `/opds/*` returns Atom XML for e-reader apps.
  Auth: HTTP Basic (`auth.BasicAuth`). Unchanged through the refactor.
- **SSE stream** — `/events` streams realtime job / state transitions
  to the browser; see §5.7.
- **SPA shell** — any unmatched GET falls back to the embedded
  `index.html` so the client router can resolve.

Shared concerns:

- **CSRF:** global Origin/Referer check on every state-changing
  request (`auth.CSRFGuard`) paired with `SameSite=Lax` cookies.
- **File serving:** reader/OPDS deliver book bytes through
  `internal/handler/files.go:serveBookFile`, which routes via
  `service.LibraryStore` + `storage.Resolver` to the right backend.
  Local backends stream from disk (legacy path-prefix sandbox still
  enforced); S3 backends either stream through the server or 302 to
  a presigned URL (`EMBOOKSHELF_PRESIGN_FALLBACK`). See §4.7.
- **File uploads:** `POST /api/v1/bookdrop/upload` accepts
  multipart, streams parts via `mime/multipart.Part.Read` (no
  full-body buffering), and writes into `BOOKDROP_PATH` so the
  shared ingest pipeline picks them up. Files also arrive via the
  watcher and via `library.scan` jobs.

### 8.1 JSON API (grouped)

`router.go` is authoritative for the exact route list. Below is the
shape, not the index. Auth column: **Public** = no gate, **Session**
= `auth.RequireAuth`, **Admin** = `RequireAuth` + `RequireRole(admin)`.

| Resource group | Auth | Endpoints (verbs) |
|----------------|------|-------------------|
| Health | Public | `GET /healthcheck` (mounted before CSRF guard) |
| Auth — local | Public | `POST /auth/login`, `POST /auth/logout`, `GET POST /auth/signup` |
| Auth — OIDC | Public | `GET /auth/oidc/config`, `GET /auth/oidc/:slug` (start), `GET /auth/oidc/callback` |
| Me + account | Session | `GET PATCH /me`, `POST /me/password`, `GET /account/identities`, `GET /account/oidc/link/:slug`, `DELETE /account/oidc/:provider`, `POST /account/password/set` |
| Instance + config | Session | `GET /instance`, `GET /config` |
| Search | Session | `GET /search` (cross-entity for command palette) |
| Libraries | Session | `GET /libraries` |
| Books | Session | `GET PATCH DELETE /books/:id`, `GET /books`, `GET /books/:id/cover`, `GET /books/:id/file`, `GET /books/:id/pages`, `GET /books/:id/pages/:n`, `POST /books/:id/progress`, `POST DELETE /books/:id/shelves/:slug`, `POST /books/:id/send/:deviceId` |
| Shelves | Session | `GET POST /shelves`, `PATCH DELETE /shelves/:slug` |
| BookDrop | Session | `GET /bookdrop`, `GET /bookdrop/:id/cover`, `POST /bookdrop/upload`, `DELETE /bookdrop/processed`, `POST /bookdrop/:id/approve`, `POST /bookdrop/:id/reject` |
| Enrichment | Session | `GET /books/:id/enrich`, `GET /books/:id/enrich/stream` (SSE), `PUT /books/:id/metadata`, `PUT /books/:id/metadata/locks`, `POST /books/metadata/isbn-lookup`, `POST /books/:id/cover-from-url` |
| Stats | Session | `GET /stats`, `GET /stats/reading` |
| Devices | Session | `GET POST /devices`, `DELETE /devices/:id` |
| Annotations | Session | `GET /annotations` (recent), `GET POST /books/:id/annotations`, `PATCH DELETE /annotations/:id` |
| Settings — libraries | Admin | `GET POST /settings/libraries`, `POST /settings/libraries/:id/rescan`, `DELETE /settings/libraries/:id` |
| Settings — providers | Admin | `GET /settings/providers`, `PATCH /settings/providers/:id` |
| Settings — metadata | Admin | `GET PUT /settings/metadata` |
| Settings — OIDC | Admin | `GET PUT /settings/oidc`, `POST /settings/oidc/test/:slug` |
| Settings — users | Admin | `GET POST /settings/users`, `PATCH /settings/users/:id/role`, `POST /settings/users/:id/approve`, `POST /settings/users/:id/deny`, `DELETE /settings/users/:id` |
| Settings — instance | Admin | `GET /settings/instance` |
| Realtime | Session | `GET /events` (SSE) |

### 8.2 OPDS 1.2 catalog (live today)

Basic Auth (`auth.BasicAuth`) under `/opds/*`.

| Method | Route | Notes |
|--------|-------|-------|
| GET | `/opds/` | Navigation feed — All / Recent / per-library links |
| GET | `/opds/all` | Acquisition feed (paged 50/page) |
| GET | `/opds/library/:slug` | Per-library acquisition feed |
| GET | `/opds/recent` | Recently added |
| GET | `/opds/search?q=...` | Full-text search results |
| GET | `/opds/search.xml` | OpenSearch description |
| GET | `/opds/book/:id/download` | Book file bytes |
| GET | `/opds/cover/:id` | Book cover image |

### 8.3 SPA fallback

| Method | Route | Notes |
|--------|-------|-------|
| GET | `/`, `/assets/*`, `/<anything>` | Served from the embedded `internal/staticfs/dist/` via a Gin `NoRoute` handler. Matching files go through `http.FileServer`; unmatched paths return `index.html` so TanStack Router can resolve client-side routes on hard reloads. `/api/*` and `/opds/*` are excluded from the fallback to preserve proper 404s. |

---

## 9. Build and Deployment

### 9.1 Multi-Stage Docker Build

Three stages: UI (`oven/bun:1` → `bun install --frozen-lockfile` +
`bun run build` + `sync-dist.ts`) → Go build → distroless runtime. See
[Dockerfile](../Dockerfile) for the authoritative recipe. The final
binary embeds the compiled SPA at `internal/staticfs/dist/` via
`//go:embed all:dist`.

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

Two processes power local development:

- **`make dev`** — Go backend on `:6060` via `go tool air`. Air watches
  `**/*.go` + `**/*.sql`; it ignores `ui/` and
  `internal/staticfs/dist/` so a React edit doesn't trigger a Go
  rebuild.
- **`make ui-dev`** — `cd ui && bun run dev`, i.e. the Vite dev server
  on `:5173` with the `@tailwindcss/vite` plugin handling CSS inline.
  Vite proxies `/api`, `/opds`, and `/events` to `:6060` (Go) and
  `/v1/*` to `:4318` (grafana/otel-lgtm) so session cookies, SSE, and
  browser OTLP traces all stay same-origin.

`make up` runs both in parallel under one `trap 'kill 0'` so Ctrl-C
tears them down together. `make up-otlp` additionally enables browser
+ backend OTel exporters against the `grafana/otel-lgtm` compose
service (Grafana UI on `:3001`).

Schema migrations apply automatically on boot
(`MIGRATE_ON_START=true` by default).

### 9.4 UI build quirks

One pragmatic workaround remains around TanStack Start's prerender
pass; it's documented here so nobody "simplifies" it away.

- **Vite's `build.outDir` stays inside `ui/`** (defaults to `dist/`).
  Redirecting it outside the package breaks Node's module resolution
  during the prerender — the SSR bundle walks up from `dist/server/`
  looking for `node_modules` and never reaches `ui/node_modules/`.
  [`ui/scripts/sync-dist.ts`](../ui/scripts/sync-dist.ts) (a bun script
  invoked by `bun run build`) is what moves the compiled shell +
  assets into `internal/staticfs/dist/` after build, duplicating
  `_shell.html` as `index.html` so Go's SPA fallback finds it.

Historical note: earlier iterations of this repo ran Tailwind via the
standalone `@tailwindcss/cli` as a side-process because `@tailwindcss/node`'s
ESM loader collided with Start's prerender on `h3-v2`/`rou3` alias
resolution. The current `@tailwindcss/vite` plugin + SPA-mode combo no
longer hits that issue — Tailwind compiles inline and there is no
generated stylesheet side-car.

---

## 10. Security Architecture

| Concern | Status | Implementation |
|---------|--------|---------------|
| Authentication | **Built** | Local session cookies + OPDS Basic Auth + OIDC/SSO (multi-provider via `user_identities`, ADR-0007). Remote Auth deferred. See §4.5. |
| Authorization | **Built** | Gin middleware guards route groups (`RequireAuth`, `RequireRole`); admin-only gate on `/api/v1/settings/*`. Per-book ACL is a single-tenant model today — every user can see every book; multi-tenant content restrictions deferred. |
| Password storage | **Built** | `golang.org/x/crypto/bcrypt`, min 8 chars. Seed admin hash generated via `pgcrypto.crypt(... gen_salt('bf'))` — same bcrypt format Go consumes. |
| Session management | **Built** | Server-side `sessions` table, 7-day TTL, slid forward in one atomic `UPDATE ... RETURNING`. Logout deletes the row. Opportunistic purge of expired sessions at boot. |
| User approval | **Built** | First-login OIDC users land in `user_approval_status=pending`; admins approve/deny via `/api/v1/settings/users/:id/approve\|deny` before the user can access the app (`/login/pending` route). |
| Secrets at rest | **Built** | Provider API keys + cookies + OIDC client secrets encrypted with AES-256-GCM under `EMBOOKSHELF_SECRET_KEY` (ADR-0010). Plaintext storage allowed in dev (no key set) but logs a warning at boot. |
| CORS | **Built** | `gin-contrib/cors`, allowed origins via `ALLOWED_ORIGINS` env var. **Presign mode for book delivery additionally requires bucket-side CORS** — without it cross-origin redirects from the SPA's `fetch`/XHR fail. |
| CSRF | **Built** | Origin/Referer match against `Host` on every non-safe method (`auth.CSRFGuard`) paired with `SameSite=Lax` cookies. Per-form tokens not needed — all state-changing requests are same-origin. |
| File-serve sandbox | **Built** | Local backends resolve the book path with `filepath.Abs` and require it to be rooted under `BOOKDROP_PATH` or the configured library `root`. S3 backends are key-scoped to the library prefix; presigned URLs are TTL-bounded (`EMBOOKSHELF_PRESIGN_TTL`, default 10m). |
| Cover-fetch SSRF protection | **Built** | `EnrichmentService.ImportCoverFromURL` allow-lists provider hosts, requires HTTPS, requires `image/*` Content-Type, caps body at 10 MB. |
| S3 orphan cleanup | **Built** | Edit-time renames keep old keys alive in `pending_orphans` until `EMBOOKSHELF_S3_RENAME_GRACE` elapses (ADR-0005), so already-issued presigned URLs don't 404 mid-download. |
| Audit trail | Planned | `audit_logs` table for action history. |
| Parental controls | Planned | `user_content_restrictions`. |
| TLS | Runtime | Terminated by the reverse proxy in production; the server listens plain HTTP inside the container. OPDS Basic Auth assumes TLS. |

---

## 11. Configuration

All configuration flows through environment variables (optionally
sourced from a `.env` file in development). Authoritative source:
[`internal/config/config.go`](../internal/config/config.go).

### Server

| Variable | Default | Purpose |
|----------|---------|---------|
| `EMBOOKSHELF_PORT` | `6060` | Application port |
| `ALLOWED_ORIGINS` | `*` | CORS origins (comma-separated) |
| `LOG_LEVEL` | `info` | `slog` level |
| `APP_URL` | — | Public origin of the instance. Feeds the OIDC redirect URI (`${APP_URL}/api/v1/auth/oidc/callback`). |
| `MIGRATE_ON_START` | `true` | Apply pending app migrations on boot. Set `false` to manage externally via `go run ./cmd/migrate up`. |

### Database

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | `postgres://localhost:5432/embookshelf` | Postgres connection DSN. A `sqlite://` DSN refuses to boot and names `embookshelf import-sqlite` (ADR-0023). |
| `DATABASE_MAX_CONNS` | `20` | pgxpool max connections |
| `DATABASE_MIN_CONNS` | `5` | pgxpool min idle connections |

### BookDrop + filesystem

| Variable | Default | Purpose |
|----------|---------|---------|
| `BOOKDROP_PATH` | `./bookdrop` | Watched folder for manual imports |
| `BOOKDROP_POLL_SECONDS` | `5` | Watcher poll interval |
| `DATA_PATH` | `./data` | Storage root for derived data — covers under `${DATA_PATH}/covers/books/` and `bookdrop/` |

### Storage (S3)

The shared S3 bucket is configured via env; per-library prefix is
computed from `libraries.slug`. When `EMBOOKSHELF_S3_BUCKET` is
empty, `kind=s3` library creation is disabled.

| Variable | Default | Purpose |
|----------|---------|---------|
| `EMBOOKSHELF_S3_BUCKET` | — | Shared bucket name. Empty disables S3 libraries. |
| `EMBOOKSHELF_S3_REGION` | `us-east-1` (when bucket set) | AWS region |
| `EMBOOKSHELF_S3_ENDPOINT` | — | Custom endpoint (MinIO, R2, B2). Auto-prepends `https://` if scheme-less. |
| `EMBOOKSHELF_S3_ACCESS_KEY_ID` | — | Static credentials |
| `EMBOOKSHELF_S3_SECRET_ACCESS_KEY` | — | Static credentials |
| `EMBOOKSHELF_S3_FORCE_PATH_STYLE` | `false` | Path-style addressing (required by MinIO and some R2 setups) |
| `EMBOOKSHELF_PRESIGN_TTL` | `10m` | Presigned URL lifetime (`time.ParseDuration`) |
| `EMBOOKSHELF_PRESIGN_FALLBACK` | `""` (= stream) | `presign` to opt into 302 redirects for book delivery; otherwise the server streams bytes |
| `EMBOOKSHELF_S3_RENAME_GRACE` | `max(2 × PresignTTL, 1h)` | Window before the orphan sweeper deletes old keys after an edit-time rename (ADR-0005) |

### Secrets

| Variable | Default | Purpose |
|----------|---------|---------|
| `EMBOOKSHELF_SECRET_KEY` | — | Base64-encoded 32-byte KEK for AES-256-GCM encryption of provider/OIDC secrets at rest (ADR-0010). Unset = plaintext storage (dev mode); the server logs a warning at boot. |
| `SESSION_SECRET` | — | *(reserved)* Not read today — sessions are server-side. Will be used when a JWT layer is added for the JSON API. |

### OpenTelemetry

When `OTEL_ENABLED=true` the server exports traces, metrics, and
logs via OTLP. Standard `OTEL_*` env vars are honored too.

| Variable | Default | Purpose |
|----------|---------|---------|
| `OTEL_ENABLED` | `false` | Master switch |
| `OTEL_SERVICE_NAME` | `embookshelf` | Resource service.name |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP collector endpoint |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | `grpc` or `http/protobuf` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | Disable TLS (for local LGTM) |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | Sample ratio (0.0–1.0) |

The browser SDK uses `VITE_OTEL_ENABLED=true` to opt into
`@opentelemetry/sdk-trace-web` (`document-load` /
`user-interaction` / `fetch` instrumentations).

Library storage roots, OIDC issuer/client/secret, provider API
keys/cookies, and instance-wide settings (signup-open, default
library, instance name) are managed in the database via the admin
Settings UI, not via env.

---

## 12. Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Monolith over microservices** | Single deployment unit simplifies self-hosting; no inter-service communication overhead. |
| **Go over JVM** | Lower memory footprint, single static binary, faster cold starts — all meaningful for self-hosters on modest hardware. |
| **React SPA (TanStack Start, SPA mode) over server-rendered Templ/HTMX** | The original Templ + HTMX stack worked, but porting rich interactions (the reader chrome, the bookdrop approval split-pane, the metadata editor with live provider matches) turned every screen into a pile of per-fragment endpoints. React gives us one component tree that the designer's prototype maps onto 1:1 and `@tanstack/react-query` handles the "stale/refetch" logic that HTMX partial swaps were manually re-implementing. The tradeoff is a client bundle the browser has to parse on first load; tree-shaking + per-route chunking keeps the first-paint cost bounded. |
| **SPA mode instead of TanStack Start SSR** | The Go binary is already the canonical server. Running a Node SSR runtime alongside it would double operational surface area without paying for itself — SEO isn't a concern for an auth-walled personal library and the first-paint win is small against cached assets. SPA mode emits a static shell + client bundle, keeps file-based routing and `createFileRoute`, and lets Go own request handling. |
| **TanStack Query over bespoke fetch + cache** | Server-state cache, invalidation, devtools, and a well-worn pattern for SSE-driven cache invalidation (once `/events` is rewired). Free upgrade path to prefetch-on-route-load via loader context. |
| **Tailwind 4 CSS-first config** | Design tokens live in one `@theme` block that both CSS and TSX components reference — no JS config, no PostCSS plugin drift. |
| **Tailwind via `@tailwindcss/vite`** | First-class Vite integration, no CLI watcher side-process, no generated stylesheet side-car. The earlier standalone-CLI workaround (to avoid the `@tailwindcss/node` loader colliding with Start's prerender on `h3-v2`/`rou3` aliases) is no longer needed under the current plugin + SPA-mode combo. See §9.4. |
| **shadcn/ui (radix-mira) over rolling our own primitives** | Forms, menus, dialogs, tabs, and toasts get battle-tested keyboard nav + ARIA + focus management + dark-mode support for free via [radix-ui](https://www.radix-ui.com) and [sonner](https://sonner.emilkowal.ski/). The editorial "built like a printed book" layer (`.cover`, `.chip`, `.shelf-plank`, Source Serif 4 typography scale) lives alongside the shadcn tokens in `styles.css`, so the custom voice survives the adoption. Components land under `components/ui/` via `bunx shadcn add` and are owned/forkable source. |
| **Bun over npm + node** | Bun's installer is ~10× faster than npm on a cold cache, it runs TS scripts (`sync-dist.ts`) directly without a compile step, and the Docker build stage is a single `oven/bun:1` image. The production binary has no JS runtime, so bun is a build-time-only dependency. |
| **PostgreSQL only (ADR-0023)** | SQLite was the zero-dependency default for single-user installs, but every SQL statement had to be written twice and the features that matter — `jsonb`, `tsvector` search, River's transactional queue, concurrent writes — are Postgres-only, so SQLite installs got degraded substitutes of each. Postgres is now required: a `sqlite://` DSN refuses to boot and names the one-shot importer, `embookshelf import-sqlite` (§6.2). The `modernc.org/sqlite` driver registration and the SQLite migration tree survive for the importer alone. |
| **Hand-written SQL, no ORM (ADR-0023)** | Explicit query surface, no N+1 surprises. The dual-dialect burden was the main argument for adopting a query layer (bun was evaluated); dropping SQLite removes it, so hand-written pgx stays on merit. sqlc remains unstaged — it generates per engine, which suited the old two-dialect world poorly and is unnecessary in a one-dialect one. |
| **golang-migrate over goose/dbmate** | Paired `.up.sql`/`.down.sql` files are unambiguous; the library is small, pgx-friendly, and can be embedded into the app binary so a single artifact can run its own migrations in any environment. |
| **Gin over chi/echo** | Rich built-in middleware (logger, recovery, CORS via `gin-contrib`), well-known binding/validation story, ergonomic `gin.Context` for streaming responses. Survived the Templ→React and React→shadcn/bun UI migrations unchanged. |
| **River for background jobs (ADR-0023)** | River gives exactly-once semantics inside the same transaction as the enqueueing mutation, plus horizontal scale and a dashboard. The `queue.Client` interface survives the collapse to one backend at one method wide: the kind travels with the payload, and `queue/registry.go` declares each job once so River's typed-worker plumbing is derived rather than hand-written. The service layer never imports river. |
| **Pluggable storage (`local` + `s3`) per library** | Self-hosters need both: laptop installs want a folder, multi-user / cloud installs want object storage. Capability bitset (`CapPresign`, `CapStorageClass`, …) lets code paths gate cleanly without leaking backend type-asserts everywhere. |
| **Stream by default, presign opt-in** | Streaming through the app server always works; presigning saves bandwidth + CPU but requires bucket-side CORS and TTL care. Make the safe choice the default; gate the optimization behind `EMBOOKSHELF_PRESIGN_FALLBACK=presign`. |
| **S3 rename = copy + deferred delete with grace window (ADR-0005)** | Edit-time folder renames must not break already-issued presigned URLs. Old keys land in `pending_orphans` until `EMBOOKSHELF_S3_RENAME_GRACE` elapses; a sweeper deletes after. Never atomic, but never racy either. |
| **`tsvector` search in the database (§4.8)** | A generated `tsv` column plus a GIN index gives cross-entity ranked search with no extra service to run, and `websearch_to_tsquery` means the user-facing query syntax is Postgres' well-worn one rather than something we invent. Dropping SQLite (ADR-0023) removed the parallel FTS5 engine that used to sit behind the same `SearchService`. |
| **Provider catalog in the binary (ADR-0008)** | Provider list, rate-limit defaults, and config schemas ship in the binary, not the DB. New providers are one catalog entry + one `Build` switch arm; rate limits don't need a migration; admins can't enable a provider the binary doesn't ship a driver for. |
| **Provider secrets AES-256-GCM at rest (ADR-0010)** | Provider API keys + cookies + OIDC client secrets live in `provider_settings_config` / `oidc_settings` encrypted under `EMBOOKSHELF_SECRET_KEY`. Plaintext allowed in dev so local hacking has no ceremony, but the server logs a warning at boot to keep the fact visible. |
| **ISBN priority chain over fan-out merge (ADR-0011)** | Free-text search merges every provider so the user picks; ISBN is canonical so the first authoritative answer wins. Ordered chain (Hardcover → Google → OpenLibrary → Amazon → Goodreads/DDG) stops on first non-empty match. |
| **Auto-enrich empty-only on first ingest (ADR-0012)** | Books that arrive with nothing but a filename get the top match auto-applied so the library doesn't fill up with `untitled-2024.epub`. Rows with any existing metadata go through the user-confirmation flow because we can't tell which fields they care about. |
| **Provider fan-out with graceful degrade (ADR-0013)** | A flaky provider should not poison the merged result. `errgroup` with no shared cancellation; failures are logged + surfaced as `provider-error` SSE frames; peers complete normally. |
| **Edit-side metadata write-back via sidecars + in-file embed (ADR-0001)** | Every metadata edit lands in three places: DB, JSON sidecar (canonical lossless round-trip), and the file itself when the format supports it. Reattach on rescan reads the sidecar so user edits survive `library.scan` even after a rename. |
| **`user_identities` table for multi-provider OIDC (ADR-0007)** | One user, many linked IdPs. Replaces a single `oidc_subject`/`oidc_issuer` pair on `users`. Account UI gates unlink with a lockout guard so the user can't strand themselves without a credential. |
| **Cover SHA-256 dedup** | `books.cover_hash` indexes content. Multiple books pointing at the same artwork — common after enrichment fan-out — share one blob in `coverstore` instead of duplicating bytes per-row. |
| **Format-specific processors** | Strategy pattern allows adding new formats without modifying existing code. |
| **Server-side sessions over stateless JWT (for the SPA)** | Revocation is free, no refresh-token rotation ceremony, and `SameSite=Lax` cookies flow naturally through both same-origin requests and the Vite dev-proxy. A JWT layer can still be added to `/api/v1/*` later for external API clients without touching the SPA auth. |
| **Basic Auth for OPDS** | E-reader apps don't carry session cookies; Basic Auth over TLS is the documented pattern and works with every OPDS client out there. |
| **Client-side EPUB/PDF readers (epub.js / PDF.js)** | Browser-native pagination + typography + `IntersectionObserver`-based lazy rendering beats server-side reflow at the implementation-cost/quality tradeoff point. The server's job stays tight: serve bytes, persist position. |
| **Path-rooted file-serve sandbox** | Single authoritative list (BOOKDROP + registered library paths) beats per-book ACL for a self-hosted single-tenant instance; adding per-book ACLs later is an additive check, not a replacement. |
| **One resume column for every reader format** | `user_book_progress.resume_cfi` carries `epubcfi(...)` (EPUB), `page:N` (PDF + comic), `time:Ns` (audio). Prefix-discriminated, no parallel column or JSONB needed. |
| **`Deps` struct for `handler.New`** | 11+ dependencies in a positional constructor was unreadable; struct literal makes call sites self-documenting and new deps additive. |
| **File-based routing with pathless `_app` layout** | The reader is a full-screen takeover with no sidebar; every other screen shares one chrome. A pathless parent expresses that structurally — the alternative (wrapping each route in a `<Layout>` component manually) drifts. |
| **Source Serif 4 + Literata + IBM Plex Mono** | Research-backed editorial stack: Source Serif 4 (Adobe, variable `opsz 8..60`, screen-tuned) for UI + display; Literata (commissioned by Google for Play Books) for the reader body via `--font-reader`; IBM Plex Mono for metadata chrome. |
