# embookshelf

Self-hosted, multi-user digital library — Go backend + React SPA
(TanStack Start) + Postgres. EPUB + PDF readers, full-text search,
per-user shelves, metadata enrichment, live BookDrop import queue, and
an OPDS 1.2 catalog for e-readers. Ships as a single binary with the
frontend embedded.

See [docs/architecture.md](docs/architecture.md) for the technical shape
and [docs/prd.md](docs/prd.md) for the product intent + roadmap.

## Layout

```
cmd/                       Go entry points
internal/                  Go backend (auth, service, repo, handler, queue, sse, …)
internal/staticfs/dist/    Embedded SPA shell (written by the frontend build)
frontend/                  TanStack Start SPA (Vite + TS + Tailwind v4)
docs/                      PRD + architecture
scripts/seed.sql           Dev seed (admin@local / changeme)
```

## Development

```bash
# One-time
make db-up           # start Postgres
make frontend-install

# Iteration — one terminal
make up              # backend (air) + Vite (concurrently) on :6060 and :5173
```

Open <http://localhost:5173/>. Vite proxies `/api`, `/opds`, `/events`
to the Go server on `:6060`, so cookies and SSE keep flowing.

If you want the processes separated (different panes, logs split):

```bash
make dev             # backend only — live-reload via `go tool air`
make frontend-dev    # Vite only — includes Tailwind CLI watcher via `concurrently`
```

First-run signup creates the admin. With the seed:

```bash
make seed            # pipes scripts/seed.sql → admin@local / changeme
```

## Production build

```bash
make build           # npm run build (frontend) → internal/staticfs/dist, then go build
./tmp/embookshelf
```

The binary ships self-contained. The compiled SPA is embedded via
`//go:embed all:dist` in
[internal/staticfs/staticfs.go](internal/staticfs/staticfs.go), so
there is no Node runtime in production.

## What's live end-to-end

- **Auth** — session cookies, bcrypt passwords, first-run admin
  bootstrap, CSRF via SameSite + Origin/Referer check.
- **Library + shelves** — multi-library model, per-user shelf CRUD,
  book-to-shelf toggle, full-text search, sort, format + shelf + library
  filters.
- **Book detail + metadata editor** — inline edits save to
  `PATCH /api/v1/books/:id`; metadata enrichment via Google Books +
  Open Library with confidence-sorted match cards and one-click "Use
  fields" / "Use cover".
- **BookDrop** — watcher polls `./bookdrop` every 5 s, ingests EPUB
  metadata + cover, surfaces the file for review, approve/reject
  promotes into a real library row. Realtime updates via SSE.
- **Readers** — EPUB (epub.js, paginated, TOC, CFI resume) and PDF
  (pdfjs-dist, lazy-rendered pages, `page:N` resume). Per-user progress
  persists across sessions.
- **Settings → Libraries** (admin) — register filesystem roots, trigger
  scans, see last-scan stats.
- **OPDS 1.2** — catalog at `/opds/*` with Basic Auth. Works with
  KOReader, Moon+ Reader, FBReader, Aldiko, Marvin, etc.
- **Realtime** — server-sent events at `/events`; background job state
  transitions invalidate react-query caches without polling.

## How the SPA is served

- **Build-time**: TanStack Start is configured in **SPA mode**
  ([frontend/vite.config.ts](frontend/vite.config.ts)). `vite build`
  produces `frontend/dist/client/{_shell.html, assets/*}`;
  [frontend/scripts/sync-dist.mjs](frontend/scripts/sync-dist.mjs)
  copies the shell + assets into `internal/staticfs/dist/` and
  duplicates `_shell.html` as `index.html` so Go's SPA fallback finds
  it.
- **Runtime**: Go serves `/api/v1/*`, `/opds/*`, `/events`, and any
  static file from the embedded FS; anything else returns `index.html`
  via a `NoRoute` handler
  ([internal/handler/router.go](internal/handler/router.go)) so the
  TanStack Router can resolve on hard reloads.

Two workarounds worth knowing:

- **Tailwind compiles via the standalone CLI**, not through the Vite
  plugin. `@tailwindcss/node`'s ESM loader conflicts with Start's
  prerender pass. `npm run css` (or `css:watch`) emits
  `frontend/src/generated.css`; `__root.tsx` imports that file with the
  `?url` suffix so it becomes a `<link rel="stylesheet">`.
- **Vite's `outDir` stays inside `frontend/`** (`dist/`). Redirecting
  it outside breaks Node's module resolution during the prerender —
  the SSR bundle can't resolve `rou3`/`h3` when it lives under
  `internal/`. The sync script is what moves the final files out.

## What's next

See [docs/prd.md § 11 — Planned](docs/prd.md) for the full greenfield
backlog. Near-term candidates:

- **OIDC + forward auth** — the single-user session flow is solid; SSO
  integrations are the next auth delta.
- **Comic + audiobook readers** — the ingest + catalog side is
  format-agnostic; the missing piece is a CBZ/M4B viewer.
- **Annotations** — `NOTES` / `book_notes` / `highlights` tables + a
  React highlight layer over epub.js / pdf.js. The Notebook view
  already renders read-only notes; wiring the write path is the
  milestone.
- **Reading-session analytics** — the Dashboard heatmap is ready and
  waiting for a real `reading_sessions` table.
