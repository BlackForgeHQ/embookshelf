# embookshelf

Self-hosted, multi-user digital library — Go backend + React SPA
(TanStack Start + shadcn/ui) + Postgres. EPUB + PDF readers, full-text
search, per-user shelves, metadata enrichment, live BookDrop import
queue, and an OPDS 1.2 catalog for e-readers. Ships as a single binary
with the UI embedded.

See [docs/architecture.md](docs/architecture.md) for the technical shape
and [docs/prd.md](docs/prd.md) for the product intent + roadmap.

## Layout

```
cmd/                       Go entry points
internal/                  Go backend (auth, service, repo, handler, queue, sse, …)
internal/staticfs/dist/    Embedded SPA shell (written by the UI build)
ui/                        TanStack Start SPA (Vite + React 19 + Tailwind v4 + shadcn/ui)
docs/                      PRD + architecture
scripts/seed.sql           Dev seed (admin@local / changeme)
```

## Development

Prerequisites: **Go 1.25**, **[Bun](https://bun.sh) 1.x**, Docker.

```bash
# One-time
make db-up           # start Postgres
make ui-install      # bun install inside ui/

# Iteration — one terminal
make up              # backend (air) + Vite on :6060 and :5173
```

Open <http://localhost:5173/>. Vite proxies `/api`, `/opds`, `/events`
to the Go server on `:6060`, so cookies and SSE keep flowing. `/v1/*`
is proxied to `:4318` for browser OTLP traces in dev (see
[compose.dev.yml](compose.dev.yml)).

If you want the processes separated (different panes, logs split):

```bash
make dev             # backend only — live-reload via `go tool air`
make ui-dev          # Vite dev server only (bun run dev)
```

First-run signup creates the admin. With the seed:

```bash
make seed            # pipes scripts/seed.sql → admin@local / changeme
```

## Production build

```bash
make build           # bun run build (ui) → internal/staticfs/dist, then go build
./tmp/embookshelf
```

The binary ships self-contained. The compiled SPA is embedded via
`//go:embed all:dist` in
[internal/staticfs/staticfs.go](internal/staticfs/staticfs.go), so
there is no Node/Bun runtime in production.

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

## UI stack

- **React 19 + TanStack Start** in SPA mode (no SSR in prod; a
  prerender pass emits the `_shell.html` entry).
- **Tailwind v4** via the first-class `@tailwindcss/vite` plugin — no
  standalone CLI watcher, no separate generated stylesheet. Design
  tokens live in [ui/src/styles.css](ui/src/styles.css) under `@theme`.
- **[shadcn/ui](https://ui.shadcn.com)** (radix-mira style) provides
  the primitive layer (Button, Dialog, Select, Switch, Tabs, Dropdown,
  Popover, Sonner toasts, …) under [ui/src/components/ui/](ui/src/components/ui/).
  Editorial overrides (`.btn`, `.chip`, `.cover`, `.t-h1`, …) live
  alongside the shadcn tokens in `styles.css`.
- **Bun** is the package manager + script runner. No `npm`/`node_modules`
  indirection in CI.
- **TanStack Query** for server state, **TanStack Router** for
  file-based routes. `useRealtime` wires the SSE stream into query
  invalidation.
- **Browser OpenTelemetry** (document-load, user-interaction, fetch
  instrumentations) ships with the app and is gated on
  `VITE_OTEL_ENABLED=true` — OTLP traces route to
  [grafana/otel-lgtm](compose.dev.yml) in dev via the `/v1` proxy.

## How the SPA is served

- **Build-time**: TanStack Start is configured in **SPA mode**
  ([ui/vite.config.ts](ui/vite.config.ts)). `vite build` produces
  `ui/dist/client/{_shell.html, assets/*}`;
  [ui/scripts/sync-dist.ts](ui/scripts/sync-dist.ts) (run via bun) copies
  the shell + assets into `internal/staticfs/dist/` and duplicates
  `_shell.html` as `index.html` so Go's SPA fallback finds it.
- **Runtime**: Go serves `/api/v1/*`, `/opds/*`, `/events`, and any
  static file from the embedded FS; anything else returns `index.html`
  via a `NoRoute` handler
  ([internal/handler/router.go](internal/handler/router.go)) so the
  TanStack Router can resolve on hard reloads.

Worth knowing:

- **Vite's `outDir` stays inside `ui/`** (`dist/`). Redirecting it
  outside breaks Node's module resolution during the prerender — the
  SSR bundle can't resolve `rou3`/`h3` when it lives under `internal/`.
  The sync script is what moves the final files out.
- **`@tailwindcss/vite` replaces the old Tailwind CLI watcher.** Earlier
  iterations of this repo ran `tailwindcss -i styles.css -o generated.css`
  as a side-process because `@tailwindcss/node` collided with Start's
  prerender; that's no longer the case under the current Vite plugin +
  SPA mode combo.

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
