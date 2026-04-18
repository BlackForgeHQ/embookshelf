# Embookshelf - Architecture Document

## 1. High-Level Architecture

Embookshelf is a Go backend + React SPA, shipped as a single binary. The
frontend is a [TanStack Start](https://tanstack.com/start) app compiled in
**SPA mode** ([Vite 7](https://vite.dev) + React 18 + TypeScript +
[Tailwind 4](https://tailwindcss.com)). The Go server exposes JSON APIs
under `/api/v1/*`, an OPDS 1.2 catalog under `/opds/*`, an SSE stream at
`/events`, and serves the compiled SPA shell embedded in the binary via
`//go:embed`. Data lives in PostgreSQL.

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
| **Database** | PostgreSQL | 16+ |
| **DB Driver** | pgx | v5 |
| **Migrations** | [golang-migrate/migrate](https://github.com/golang-migrate/migrate) | 4.x |
| **Sessions** | Hand-rolled `sessions` table (see §4.5) | — |
| **Passwords** | `golang.org/x/crypto/bcrypt` | — |
| **Background Jobs** | [riverqueue](https://riverqueue.com/) — Postgres-backed | 0.34 |
| **Real-time** | Server-Sent Events via Gin handler | — |
| **Live reload (dev)** | [air-verse/air](https://github.com/air-verse/air) via `go tool air` | 1.65 |
| **Containerization** | Docker (multi-stage) | — |

### Frontend

| Layer | Technology | Version |
|-------|-----------|---------|
| **Framework** | [TanStack Start](https://tanstack.com/start) (SPA mode) | 1.167+ |
| **Router** | [@tanstack/react-router](https://tanstack.com/router) (file-based routes) | 1.x |
| **Server state** | [@tanstack/react-query](https://tanstack.com/query) | 5.59+ |
| **UI runtime** | React + ReactDOM | 18.3 |
| **Language** | TypeScript | 5.6 |
| **Bundler** | [Vite](https://vite.dev) | 7.x |
| **Styling** | Tailwind CSS 4 (compiled via standalone CLI, not the Vite plugin — see §9.4) | 4.2 |
| **Dev runner** | `concurrently` (Tailwind CLI watcher + Vite dev server) | 9.x |

Testing stacks for both sides are not yet wired up — planned: Go
`testing` + `testify` + `pgtest`, Playwright for e2e once the JSON API
lands.

---

## 3. Project Structure

```
embookshelf/
├── cmd/
│   ├── embookshelf/                # main.go — composition root
│   └── migrate/                    # CLI around internal/migrator (up/down/version/force)
│
├── internal/
│   ├── handler/                    # HTTP handlers (see §4.1)
│   │   ├── handler.go              # Handler struct + Deps
│   │   ├── router.go               # gin.Engine assembly + SPA fallback
│   │   ├── health.go               # /api/v1/healthcheck
│   │   ├── opds.go                 # /opds/* catalog (Atom XML)
│   │   └── files.go                # mimeForFormat, serveBookFile, parseIntOr, clampInt
│   │
│   ├── service/                    # Business logic (unchanged through the refactor)
│   │   ├── auth.go                 # Login/Logout/Verify/Signup + session TTL
│   │   ├── bookdrop.go             # ingest state machine + SSE broadcast
│   │   ├── enrichment.go           # metadata fan-out + cover-from-URL
│   │   ├── library.go              # book lookup + update
│   │   ├── library_path.go         # filesystem roots per library
│   │   ├── progress.go             # per-user reading progress
│   │   └── shelf.go                # per-user shelves CRUD
│   │
│   ├── repo/                       # pgx-backed repositories (hand-written SQL)
│   │   └── queries/                # *.sql staged for a future sqlc pass
│   │
│   ├── model/                      # Domain structs
│   ├── auth/                       # context, password, session cookie, middleware, basic
│   ├── coverstore/                 # filesystem store for extracted cover images
│   ├── fileproc/                   # Format processors — EPUB today; PDF/CBX/audio planned
│   ├── ingest/                     # BookDrop folder watcher (polling)
│   ├── middleware/                 # pagination helpers
│   ├── migrator/                   # embedded migrations + golang-migrate wrapper
│   │   └── migrations/             # NNN_name.up.sql / .down.sql
│   ├── opds/                       # Atom/XML feed types + builder
│   ├── provider/                   # Google Books + Open Library metadata providers
│   ├── queue/                      # river client wrapper
│   ├── sse/                        # Server-Sent Events fan-out hub (handler not mounted yet)
│   ├── staticfs/                   # //go:embed all:dist — embeds the compiled React SPA
│   │   ├── staticfs.go
│   │   └── dist/                   # Populated by `npm run build` (git-ignored)
│   ├── task/                       # river workers — BookDropWorker, LibraryScanWorker
│   └── config/                     # env loading
│
├── frontend/                       # TanStack Start SPA (Vite + TS + Tailwind 4)
│   ├── src/
│   │   ├── routes/                 # File-based routing — routeTree.gen.ts auto-generated
│   │   │   ├── __root.tsx          # html/body shell (HeadContent + Scripts + QueryClient)
│   │   │   ├── _app.tsx            # Pathless layout: Sidebar + main + status bar
│   │   │   ├── _app.index.tsx      # /           — Dashboard
│   │   │   ├── _app.library.tsx    # /library    — LibraryView (shelf|grid|list)
│   │   │   ├── _app.book.$id.tsx   # /book/:id   — BookDetail
│   │   │   ├── _app.book.$id.edit.tsx  # /book/:id/edit — MetadataEditor
│   │   │   ├── _app.notebook.tsx   # /notebook   — cross-book notes
│   │   │   ├── _app.bookdrop.tsx   # /bookdrop   — import review queue
│   │   │   ├── _app.settings.tsx   # /settings
│   │   │   ├── read.$id.tsx        # /read/:id   — full-screen Reader (no sidebar)
│   │   │   └── login.tsx           # /login      — stub until auth endpoints land
│   │   ├── components/
│   │   │   ├── Icon.tsx            # 46-icon set from the prototype
│   │   │   ├── Cover.tsx           # Cover + Spine + StarRating
│   │   │   ├── Sidebar.tsx         # Wired to router Links + useRouterState
│   │   │   └── TopBar.tsx
│   │   ├── data/
│   │   │   └── mock.ts             # Typed mock dataset — replaced per-route by useQuery
│   │   ├── api/
│   │   │   └── client.ts           # fetch wrapper with credentials: include
│   │   ├── router.tsx              # getRouter() — creates the QueryClient + router
│   │   ├── styles.css              # Tailwind entry (@source globs + @theme tokens)
│   │   └── generated.css           # Tailwind CLI output (git-ignored)
│   ├── scripts/
│   │   └── sync-dist.mjs           # Copies dist/client → ../internal/staticfs/dist
│   ├── index.html
│   ├── vite.config.ts              # tanstackStart({ spa: { enabled: true } })
│   ├── tsconfig.json
│   └── package.json
│
├── scripts/seed.sql                # Dev seed — admin@local / changeme
├── docs/                           # This doc + prd.md
├── Dockerfile                      # Multi-stage (frontend build → go build → distroless)
├── compose.dev.yml                 # Development Postgres
├── go.mod                          # Go 1.25; `tool` directive for air
├── Makefile                        # db-up, frontend-install, dev, frontend-dev, up, build, ...
├── .air.toml                       # live-reload config (excludes frontend/ and dist/)
└── README.md
```

Deferred scaffolding (not yet created): `compose.example.yml`,
`deploy/helm/`, `.github/workflows/` — spelled out in §9 so the shape is
documented before the folders exist.

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
  1. `/api/v1/*` — JSON. Auth, me, libraries, shelves, books (list /
     detail / PATCH / cover / file / progress / enrich / cover-from-url
     / shelf membership), bookdrop (list / cover / approve / reject),
     settings (admin-only library-path CRUD + scan trigger).
  2. `/opds/*` — Atom XML for e-readers, Basic-Auth-protected.
  3. `/events` — Server-Sent Events stream, cookie-authed.
  4. `/` (fall-through) — serves the embedded React SPA.

  Dependencies land in a `Deps` struct so `handler.New`'s signature
  stays flat as the app grows (11 deps today; positional args would be
  unreadable). Responses are pure JSON on `/api/v1/*`, XML on OPDS,
  `text/event-stream` on `/events` — no HTMX-aware content negotiation.
- **Services** — Business logic. Plain Go structs wired with constructor
  functions in `cmd/embookshelf/main.go`. The seven services
  (`AuthService`, `LibraryService`, `ShelfService`, `BookDropService`,
  `ProgressService`, `EnrichmentService`, `LibraryPathService`) are
  framework-agnostic — they predated the React rewrite and carried over
  unchanged.
- **Repositories** — `pgx/v5` with hand-written SQL. Queries are ready
  to migrate to `sqlc` once the schema stabilizes; `sqlc.yaml` +
  `internal/repo/queries/*.sql` are staged for that pass.
- **DTOs** — Request/response structs live alongside handlers
  (`userDTO`, `libraryDTO`, `bookDTO`, `bookDetailDTO`, `shelfDTO`,
  `bookdropDTO`, `enrichMatchDTO`, `settingsLibraryDTO` + pointer-field
  PATCH types). camelCase on the wire; matching TS types under
  [frontend/src/api/](../frontend/src/api/).

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
- The **SSE hub** (`internal/sse/`) broadcasts per-item updates.
  `BookDropService` calls `hub.Broadcast` on every state transition.
  The [`/events` handler](../internal/handler/events.go) subscribes
  each connected browser to the hub, sends a 25-second heartbeat
  (`: ping`) to defeat idle proxy timeouts, and tears down on client
  disconnect. The React side (`useRealtime`) opens a single
  `EventSource` inside the authed layout and dispatches each named
  event into react-query cache invalidations — see §5.7.
- Current job progress lives on the domain row itself (e.g.
  `bookdrop_items.state`/`progress`/`error_msg`) rather than a generic
  `task_history` table. A dedicated task-history table is deferred until we
  have multiple long-running job kinds to unify.

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
       │ cookie   │ │ (extern) │ │  Auth      │
       │ (local)  │ │ (planned)│ │ (planned)  │
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
- **OIDC** *(planned)* — `coreos/go-oidc` for discovery +
  `golang.org/x/oauth2` for the authorization-code flow. Backchannel
  logout endpoint to follow.
- **Remote/Forward Auth** *(planned)* — middleware that trusts
  `Remote-User` / `Remote-Email` / `Remote-Groups` reverse-proxy
  headers when `REMOTE_AUTH_ENABLED=true`.
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

---

## 5. Frontend Architecture

### 5.1 TanStack Start in SPA mode

The frontend is a [TanStack Start](https://tanstack.com/start) app
configured in **SPA mode** — no SSR, no Node runtime in production. Why
Start over vanilla `@tanstack/react-router`? File-based routing,
generated typed routes, `createFileRoute` with loaders and
`validateSearch`, and a clear upgrade path if we ever need partial
prerendering. SPA mode is opted in via the Vite plugin:

```ts
// frontend/vite.config.ts
tanstackStart({ spa: { enabled: true } })
```

Start still runs a prerender pass during build to emit `_shell.html`
(the static entry point); the sync script duplicates that file as
`index.html` so Go's SPA fallback finds it.

The route tree is regenerated on every dev change and build —
`frontend/src/routeTree.gen.ts` is git-ignored. TypeScript module
augmentation in `frontend/src/main.tsx` teaches the type system the
shape of our router so `<Link to>`, `useParams`, `useSearch`, and
`navigate` are all typed end-to-end.

### 5.2 Route tree

Routes are file-based. A leading underscore makes a **pathless layout
parent** whose children inherit the layout but not a URL segment; dots
stand in for nested folders.

| File | URL | Component |
|------|-----|-----------|
| `__root.tsx` | — | html/body shell, HeadContent, Scripts, QueryClientProvider |
| `_app.tsx` | — | Sidebar + `<main>` + status bar layout, `<Outlet />` in the middle |
| `_app.index.tsx` | `/` | Dashboard (currently reading, 12-week heatmap, stats, libraries) |
| `_app.library.tsx` | `/library` | LibraryView — shelf/grid/list, filter, sort; `?shelf=`, `?layout=` in search |
| `_app.book.$id.tsx` | `/book/:id` | BookDetail — overview / notes / annotations / versions / activity tabs |
| `_app.book.$id.edit.tsx` | `/book/:id/edit` | MetadataEditor |
| `_app.notebook.tsx` | `/notebook` | Cross-book notes + highlights |
| `_app.bookdrop.tsx` | `/bookdrop` | Import review queue (list + detail split) |
| `_app.settings.tsx` | `/settings` | Settings hub — Account section live, others stubbed |
| `read.$id.tsx` | `/read/:id` | Full-screen Reader — intentionally outside `_app` so the sidebar is hidden |
| `login.tsx` | `/login` | Stub until auth endpoints return |

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
- **Fetch wrapper** — `frontend/src/api/client.ts`. Always
  `credentials: 'include'` (the session cookie must ride along),
  JSON-body detection, uniform error shape `{ status, message }`.
- **Search params** — routes that care about URL state declare a
  `validateSearch` zod-free parser so the types are tight without an
  extra runtime dep (see [`_app.library.tsx`](../frontend/src/routes/_app.library.tsx)).
- **Mock data** — every route is currently fed by the mock dataset in
  [`frontend/src/data/mock.ts`](../frontend/src/data/mock.ts) so the
  visuals can be exercised before the JSON API lands. Each mock export
  has a typed counterpart the API will mirror.
- **Auth state** — intentionally not centralised yet. Once the JSON
  auth endpoints are back, `useQuery({ queryKey: ['me'] })` against
  `/api/v1/me` plus route-level loaders calling
  `router.invalidate()` on login/logout is the expected pattern.

### 5.4 Component library

Shared pieces live in `frontend/src/components/`:

- [`Icon.tsx`](../frontend/src/components/Icon.tsx) — 46 icons from
  the prototype, typed `IconName` union, stroke 1.5 editorial style.
- [`Cover.tsx`](../frontend/src/components/Cover.tsx) —
  `Cover`, `Spine`, `StarRating`. `Cover` takes `{ book, size, onClick, style }`;
  switches on `book.style` for 5 typographic cover styles, on
  `book.palette` for 10 bookcloth-inspired colors, and on `size` for
  xs/sm/md/lg/hero. Placeholder books render as diagonal-stripe paper
  tile.
- [`Sidebar.tsx`](../frontend/src/components/Sidebar.tsx) — router-aware
  navigation.
- [`TopBar.tsx`](../frontend/src/components/TopBar.tsx) — sticky header
  with title + subtitle + crumbs + search + right slot, reused across
  most in-app views.

### 5.5 Styling: Tailwind 4

Tailwind 4's **CSS-first configuration** drives the design system.
[`frontend/src/styles.css`](../frontend/src/styles.css) holds the
`@source` glob, `@theme` tokens, and a large `@layer components` block
for the custom primitives that are awkward as utilities:

```css
@import url('https://fonts.googleapis.com/css2?family=Source+Serif+4:ital,opsz,wght@...&family=Literata:ital,opsz,wght@...&family=IBM+Plex+Mono:wght@400;500;600&display=swap');
@import "tailwindcss";

@source "./**/*.{ts,tsx}";

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

  --font-serif:  "Source Serif 4", Georgia, serif;   /* UI + display */
  --font-reader: "Literata",       Georgia, serif;   /* long-form reading */
  --font-mono:   "IBM Plex Mono",  ui-monospace, monospace;
}

@layer components {
  .btn, .chip, .input, .cover, .cov-*, .shelf-plank, .progress, .kbd,
  .sidebar, .status-bar, .tweaks-panel, .fade-in, .t-h1, .t-h2, .t-label,
  ... /* ~600 lines */
}
```

Typography stack:

- **Source Serif 4** (Adobe, variable `opsz 8..60`) for UI + display.
- **Literata** (commissioned by Google for Play Books) for the reader
  body via `--font-reader`.
- **IBM Plex Mono** for metadata chrome.

Fonts are pulled from Google Fonts today; self-hosting them under
`internal/staticfs/dist/fonts/` is a planned follow-up to drop the
network dependency on first paint.

### 5.6 Built-in readers

| Reader | Status | Notes |
|--------|--------|-------|
| EPUB | **Built** | [`EpubReader`](../frontend/src/components/EpubReader.tsx) wraps epub.js with an imperative handle (`next` / `prev` / `goTo`). Paginated flow, `book.locations.generate(1024)` powers the percentage scrubber, `relocated` event reports `{percent, cfi}`. Typography overrides via `rendition.themes.default` so font/size changes survive chapter transitions. TOC tree flattened for the Contents panel. |
| PDF | **Built** | [`PdfReader`](../frontend/src/components/PdfReader.tsx) uses pdfjs-dist 5. Worker URL resolved via `new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url)` so Vite emits a hashed worker. Scroll container with one `<canvas>` per page; only current ± 1 page are rasterized (others cleared) to keep memory flat on large PDFs. |
| CBX (`.cbr` / `.cbz` / `.cb7`) | Deferred | Server returns per-page image URLs; client viewer with keyboard nav + manga mode. |
| Audiobook (MP3/M4B) | Deferred | Native `<audio>` with chapter navigation. |

Progress lives in `user_book_progress` per user:
`{percent, resume_cfi, last_read_at}`. EPUB writes CFI strings
(`epubcfi(...)`); PDF writes `page:N` tokens. The prefix disambiguates
them — one column suffices for both reader types.
[`read.$id.tsx`](../frontend/src/routes/read.$id.tsx) dispatches to
the right component by `book.format`, debounces progress writes by
600 ms, and flushes any pending tick via a raw `fetch` inside the
cleanup effect so a short reading session still records progress.

Reader typography (font, size, line-height) is hard-coded to
sensible defaults today. Promoting it to `reader_preferences` + a
React settings pane is a trivial future addition once the preference
set grows beyond three values.

### 5.7 Realtime (SSE)

[`useRealtime`](../frontend/src/api/realtime.ts) is mounted once inside
the authed layout (`_app.tsx`). It opens a single
`EventSource('/events', { withCredentials: true })` per session — the
browser reuses the same cookie that the JSON API calls carry, so no
separate handshake is needed.

Event → cache-invalidation dispatch is a typed `Record<RealtimeEvent,
Handler>` map; TypeScript enforces exhaustive coverage, so adding a
new event name surfaces missing handlers at build time. Today only
`bookdrop.updated` is published; it invalidates the bookdrop queue,
the books list, and the libraries list (for book-count updates after
approvals).

The native `EventSource` handles reconnection + exponential backoff
automatically; the hook only wires teardown (`removeEventListener` +
`es.close()`) on unmount. A future slice could add a connection-status
indicator for cases where the reverse proxy severs the stream with no
retry budget left.

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
- **File serving:** reader/OPDS stream book files through a path
  sandbox (`internal/handler/files.go:serveBookFile`) that allows only
  `BOOKDROP_PATH` + registered `library_paths` roots
  (trailing-separator prefix match).
- **File uploads:** will stream via `mime/multipart.Part.Read` — no
  full-body buffering for large archives. (No user-facing upload UI
  yet; files arrive via the `BOOKDROP_PATH` watcher or a `library.scan`
  job.)

### 8.1 JSON API

All routes below are live. Response bodies are camelCase JSON; error
envelope is `{ "error": "…" }`. `Auth` column abbreviates the auth
stack: **Public** = no gate, **Session** = `auth.RequireAuth`, **Admin**
= `RequireAuth` + `RequireRole(admin)`.

| Method | Route | Auth | Purpose |
|--------|-------|------|---------|
| GET | `/api/v1/healthcheck` | Public | `{"status":"ok","diskMode":"LOCAL"}`. Mounted before the CSRF guard for orchestrator probes. |
| GET | `/api/v1/auth/signup` | Public | `{ "enabled": bool }` — the SPA uses this to decide whether the signup form is visible. |
| POST | `/api/v1/auth/signup` | Public | First-run admin creation. 403 once `users` is non-empty. |
| POST | `/api/v1/auth/login` | Public | Create session, set cookie, return user DTO. |
| POST | `/api/v1/auth/logout` | Public | Destroy session + clear cookie. 204. |
| GET | `/api/v1/me` | Session | Current user — boot-time auth gate in the SPA. |
| GET | `/api/v1/libraries` | Session | All libraries with book counts. |
| GET | `/api/v1/shelves` | Session | Per-user shelves with book counts. |
| POST | `/api/v1/shelves` | Session | Create shelf (`{ name, accent? }`). Repo slugifies. |
| DELETE | `/api/v1/shelves/:slug` | Session | Delete shelf. |
| GET | `/api/v1/books` | Session | List with `?q=&sort=&format=&library=&shelf=`. Cap 500 today. |
| GET | `/api/v1/books/:id` | Session | Book detail + shelf membership. |
| PATCH | `/api/v1/books/:id` | Session | Partial metadata update (pointer-field DTO). |
| GET | `/api/v1/books/:id/cover` | Session | Cover image bytes; 404 falls through to the stylized tile. |
| GET | `/api/v1/books/:id/file` | Session | EPUB/PDF bytes via the path sandbox. |
| POST | `/api/v1/books/:id/progress` | Session | `{ progress: 0..1, resumeCfi? }`. |
| POST | `/api/v1/books/:id/shelves/:slug` | Session | Add to shelf (idempotent). |
| DELETE | `/api/v1/books/:id/shelves/:slug` | Session | Remove from shelf. |
| GET | `/api/v1/books/:id/enrich?title=&author=&isbn=` | Session | Provider fan-out; returns `{ query, matches }`. |
| POST | `/api/v1/books/:id/cover-from-url` | Session | Allow-list-protected cover import. |
| GET | `/api/v1/bookdrop` | Session | Full ingest queue (every state). |
| GET | `/api/v1/bookdrop/:id/cover` | Session | Pre-approval cover preview. |
| POST | `/api/v1/bookdrop/:id/approve` | Session | Optional `{ libraryId }`; returns the freshly-imported book detail. |
| POST | `/api/v1/bookdrop/:id/reject` | Session | Mark dismissed, delete pre-approval cover. 204. |
| GET | `/api/v1/settings/libraries` | Admin | Libraries + registered paths + scan stats. |
| POST | `/api/v1/settings/libraries/paths` | Admin | Register filesystem root `{ libraryId, path }`. |
| POST | `/api/v1/settings/libraries/paths/:id/scan` | Admin | Enqueue a `library.scan` job. 202. |
| DELETE | `/api/v1/settings/libraries/paths/:id` | Admin | Remove scan source (books stay). 204. |
| GET | `/events` | Session | Server-Sent Events stream (bookdrop state transitions today; extensible). |

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

Three stages: frontend (npm install + `vite build` + sync script) → Go
build → distroless runtime. See [Dockerfile](../Dockerfile) for the
authoritative recipe. The final binary embeds the compiled SPA at
`internal/staticfs/dist/` via `//go:embed all:dist`.

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
  `**/*.go` + `**/*.sql`; it ignores `frontend/` and
  `internal/staticfs/dist/` so a React edit doesn't trigger a Go
  rebuild.
- **`make frontend-dev`** — `cd frontend && npm run dev`. Under the
  hood that's `concurrently` running the Tailwind CLI watcher
  (`src/styles.css` → `src/generated.css`) and the Vite dev server on
  `:5173`. Vite proxies `/api`, `/opds`, and `/events` to `:6060` so
  session cookies and SSE work against the Go backend.

`make up` runs both in parallel under one `trap 'kill 0'` so Ctrl-C
tears them down together.

Schema migrations apply automatically on boot
(`MIGRATE_ON_START=true` by default).

### 9.4 Frontend build quirks

Two pragmatic workarounds exist around TanStack Start's prerender pass;
they're documented here so nobody "simplifies" them away.

1. **Tailwind via the standalone CLI, not the Vite plugin.**
   `@tailwindcss/node` installs a Node ESM loader that short-circuits
   module resolution during the prerender, which breaks on the
   `h3-v2`/`rou3` aliases that Start's SSR bundle imports. Running
   `@tailwindcss/cli` as a separate step (`npm run css` /
   `npm run css:watch`) avoids attaching the loader at all. `__root.tsx`
   imports the generated file with `?url` so Vite treats it as a
   stylesheet asset.
2. **Vite's `build.outDir` stays inside `frontend/`** (defaults to
   `dist/`). Redirecting it outside the package breaks Node's module
   resolution during the prerender — the SSR bundle walks up from
   `dist/server/` looking for `node_modules` and never reaches
   `frontend/node_modules/`.
   [`frontend/scripts/sync-dist.mjs`](../frontend/scripts/sync-dist.mjs)
   is what moves the compiled shell + assets into
   `internal/staticfs/dist/` after build, duplicating `_shell.html` as
   `index.html` so Go's SPA fallback finds it.

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
| **Monolith over microservices** | Single deployment unit simplifies self-hosting; no inter-service communication overhead. |
| **Go over JVM** | Lower memory footprint, single static binary, faster cold starts — all meaningful for self-hosters on modest hardware. |
| **React SPA (TanStack Start, SPA mode) over server-rendered Templ/HTMX** | The original Templ + HTMX stack worked, but porting rich interactions (the reader chrome, the bookdrop approval split-pane, the metadata editor with live provider matches) turned every screen into a pile of per-fragment endpoints. React gives us one component tree that the designer's prototype maps onto 1:1 and `@tanstack/react-query` handles the "stale/refetch" logic that HTMX partial swaps were manually re-implementing. The tradeoff is a bundle (~270 KB gzipped today) the browser has to parse on first load. |
| **SPA mode instead of TanStack Start SSR** | The Go binary is already the canonical server. Running a Node SSR runtime alongside it would double operational surface area without paying for itself — SEO isn't a concern for an auth-walled personal library and the first-paint win is small against cached assets. SPA mode emits a static shell + client bundle, keeps file-based routing and `createFileRoute`, and lets Go own request handling. |
| **TanStack Query over bespoke fetch + cache** | Server-state cache, invalidation, devtools, and a well-worn pattern for SSE-driven cache invalidation (once `/events` is rewired). Free upgrade path to prefetch-on-route-load via loader context. |
| **Tailwind 4 CSS-first config** | Design tokens live in one `@theme` block that both CSS and TSX components reference — no JS config, no PostCSS plugin drift. |
| **Tailwind via standalone CLI, not `@tailwindcss/vite`** | The Vite plugin's Node ESM loader collides with TanStack Start's prerender pass (breaks `h3-v2`/`rou3` alias resolution). The CLI is a clean side-process. See §9.4. |
| **PostgreSQL over MariaDB/SQLite** | `jsonb`, `tsvector` full-text search, and a mature job-queue ecosystem (river) more than earn the operational overhead. |
| **sqlc-staged over ORM** | Typed, compile-time-checked SQL keeps the query surface explicit and avoids N+1 surprises; hand-written pgx today, `sqlc.yaml` + `internal/repo/queries/*.sql` staged for when schema stabilizes. |
| **golang-migrate over goose/dbmate** | Paired `.up.sql`/`.down.sql` files are unambiguous; the library is small, pgx-friendly, and can be embedded into the app binary so a single artifact can run its own migrations in any environment. |
| **Gin over chi/echo** | Rich built-in middleware (logger, recovery, CORS via `gin-contrib`), well-known binding/validation story, ergonomic `gin.Context` for streaming responses. Survived the frontend refactor unchanged. |
| **river over custom worker pool** | Jobs live in the same Postgres transaction boundary as the mutations that enqueue them; exactly-once semantics without extra infrastructure. |
| **Format-specific processors** | Strategy pattern allows adding new formats without modifying existing code. |
| **NETWORK storage mode** | Safe degradation for NAS users rather than risking file corruption. |
| **Server-side sessions over stateless JWT (for the SPA)** | Revocation is free, no refresh-token rotation ceremony, and `SameSite=Lax` cookies flow naturally through both same-origin requests and the Vite dev-proxy. A JWT layer can still be added to `/api/v1/*` later for external API clients without touching the SPA auth. |
| **Basic Auth for OPDS** | E-reader apps don't carry session cookies; Basic Auth over TLS is the documented pattern and works with every OPDS client out there. |
| **Client-side EPUB/PDF readers (epub.js / PDF.js)** | Browser-native pagination + typography + `IntersectionObserver`-based lazy rendering beats server-side reflow at the implementation-cost/quality tradeoff point. The server's job stays tight: serve bytes, persist position. |
| **Path-rooted file-serve sandbox** | Single authoritative list (BOOKDROP + registered library paths) beats per-book ACL for a self-hosted single-tenant instance; adding per-book ACLs later is an additive check, not a replacement. |
| **CFI + `page:N` resume tokens share one DB column** | Unambiguous by prefix (`epubcfi(...)` vs `page:42`); avoids a parallel column or JSONB for a two-format reader today. |
| **`Deps` struct for `handler.New`** | 11+ dependencies in a positional constructor was unreadable; struct literal makes call sites self-documenting and new deps additive. |
| **File-based routing with pathless `_app` layout** | The reader is a full-screen takeover with no sidebar; every other screen shares one chrome. A pathless parent expresses that structurally — the alternative (wrapping each route in a `<Layout>` component manually) drifts. |
| **Source Serif 4 + Literata + IBM Plex Mono** | Research-backed editorial stack: Source Serif 4 (Adobe, variable `opsz 8..60`, screen-tuned) for UI + display; Literata (commissioned by Google for Play Books) for the reader body via `--font-reader`; IBM Plex Mono for metadata chrome. |
