# embookshelf

Self-hosted, multi-user digital library — Go backend + React SPA
(TanStack Start + shadcn/ui) + Postgres. EPUB + PDF readers, full-text
search, per-user shelves + annotations, metadata enrichment across four
providers, live BookDrop import queue, OIDC/SSO (Google / GitHub /
generic), reMarkable device sync, and an OPDS 1.2 catalog for e-readers.
Ships as a single binary with the UI embedded.

See [docs/architecture.md](docs/architecture.md) for the technical shape
and [docs/prd.md](docs/prd.md) for the product intent + roadmap.

## Layout

```
cmd/                       Go entry points
internal/                  Go backend (auth, service, repo, handler, queue, sse, …)
internal/migrator/         Embedded golang-migrate migrations
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

### Auth
- Session cookies, bcrypt passwords, first-run admin bootstrap, CSRF
  via SameSite + Origin/Referer check.
- **OIDC / SSO** — Google, GitHub, and a generic OIDC provider wired as
  three independent, parallel configs. PKCE (S256) on every flow,
  server-held state cache with 5 min TTL, shared `/callback` routed by
  the slug embedded in state. Admin `Settings → OIDC / SSO` configures
  credentials, claim mappings, auto-provisioning policy, and an
  optional "force SSO" mode that hides the password form (escape hatch
  at `/login?local=true`).
- Redirect URIs fall back to the live request's scheme + host when
  `APP_URL` is unset, so local dev and self-hosted deployments behind a
  reverse proxy work without extra env wiring.

### Library, shelves, reading
- Multi-library model with **one filesystem root per library**, fixed
  at creation. Per-user shelf CRUD, book-to-shelf toggle, full-text
  search (tsvector over title + author + series + description), sort,
  format + shelf + library filters.
- **Smart shelves** — rule-based auto-populated shelves alongside
  hand-curated ones, both styled with an accent picker.
- **Readers** — EPUB (epub.js, paginated, TOC, CFI resume) and PDF
  (pdfjs-dist, lazy-rendered pages, `page:N` resume). Per-user
  progress + reading sessions persist across sessions, feed the Stats
  heatmap, and broadcast via SSE.
- **Annotations** — highlights + margin notes with a React highlight
  layer over epub.js; recent-activity feed in the Notebook view.

### Metadata
- **Editor** — the Edit-metadata page covers the full surface: title,
  subtitle, authors, description, language, publish date, year, pages,
  ISBN-10 + ISBN-13, publisher, series (name / #/ total), genres,
  moods, tags, age rating, content rating, and a tri-state public
  review toggle. Inline edits save to `PATCH /api/v1/books/:id`.
- **Enrichment** across **four providers** — Google Books, Open
  Library, Amazon (ISBN-10 cover fallback), and DuckDuckGo (Wikipedia
  summary). Confidence-sorted match cards with one-click "Use fields"
  / "Use cover". Results are cached in-process for 5 min and a
  per-provider 60 s cooldown kicks in on 429, so repeated refetches
  don't burn rate limits.
- **File naming patterns** — per-library pattern + instance-wide
  default. Placeholders (`{title}`, `{authors}`, `{seriesIndex}`, …),
  optional blocks (`<…>`), else clauses (`<…|fallback>`), and value
  modifiers (`:first`, `:sort`, `:initial`, `:upper`, `:lower`). The
  admin-side reference doc and the resolver's Go unit tests share the
  same example set, so docs can't drift from behavior. Approved
  BookDrop files are moved under the library root via the resolved
  path.

### BookDrop + covers
- Watcher polls `./bookdrop` every 5 s, extracts EPUB/PDF metadata +
  cover, surfaces the file for review, approve/reject promotes into a
  real library row and moves the file to its target path.
- Cover images render as real `<img>` (with lazy-loading + onError
  fallback to the typographic tile) once the backend has the
  extracted bytes.
- **Format badges** on every cover (shadcn Badge) — EPUB, PDF, CBZ,
  MOBI, FB2, TXT — color-coded per format.

### Settings (admin-only)
Left-nav panels at `/settings`:
- **Libraries** — create/delete, path is immutable, trigger rescans,
  see last-scan stats, typed-name confirmation on delete.
- **File naming patterns** — default pattern + per-library overrides
  with live preview; full reference docs for placeholders, conditional
  blocks, else clauses, value modifiers, and worked examples.
- **Metadata providers** — toggle Google Books / Open Library /
  Amazon / DuckDuckGo individually. All four are enabled by default on
  fresh installs.
- **Email delivery**, **Users & roles**, **OIDC / SSO**, **Backups**,
  **About**.

Per-user preferences live in a dialog opened from the sidebar footer
dropdown (Account / Reading preferences / Device sync / Sign out) — no
route switch.

### Device sync
- **reMarkable Paper Pro** pairing with a one-time code; push any book
  to a paired device from the book detail page. OPDS is still the
  vendor-neutral fallback.

### Platform
- **OPDS 1.2** catalog at `/opds/*` with Basic Auth. Works with
  KOReader, Moon+ Reader, FBReader, Marvin, Aldiko, etc.
- **Realtime** — server-sent events at `/events`; background job state
  transitions invalidate TanStack Query caches without polling.
- **Notifications** — sonner toasts for every mutation (library create,
  rescan, pattern save, role change, OIDC save, OIDC test, library
  delete, …). No stale inline banners.
- **River** queue runs the library-scan workers; its migrations run
  alongside the app schema on boot when `MIGRATE_ON_START=true`.

## UI stack

- **React 19 + TanStack Start** in SPA mode (no SSR in prod; a
  prerender pass emits the `_shell.html` entry).
- **Tailwind v4** via the first-class `@tailwindcss/vite` plugin — no
  standalone CLI watcher, no separate generated stylesheet. Design
  tokens live in [ui/src/styles.css](ui/src/styles.css) under `@theme`.
- **[shadcn/ui](https://ui.shadcn.com)** (radix-mira style) provides
  the primitive layer (Button, Dialog, Select, Switch, Tabs, Dropdown,
  Popover, Badge, Sonner toasts, Breadcrumb, …) under
  [ui/src/components/ui/](ui/src/components/ui/). Editorial overrides
  (`.btn`, `.chip`, `.cover`, `.t-h1`, …) live alongside the shadcn
  tokens in `styles.css`.
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

## Environment

All env vars are optional unless marked required; sensible defaults
live in [internal/config/config.go](internal/config/config.go).

| Var | Default | Notes |
| --- | --- | --- |
| `DATABASE_URL` | `postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable` | PG connection string |
| `EMBOOKSHELF_PORT` | `6060` | HTTP listen port |
| `ALLOWED_ORIGINS` | `*` | CSRF allow-list for `Origin`/`Referer` |
| `SESSION_SECRET` | _(empty — dev only)_ | Sign session cookies; set in prod |
| `BOOKDROP_PATH` | `./bookdrop` | Watched folder for imports |
| `DATA_PATH` | `./data` | Covers + on-disk caches |
| `APP_URL` | _(unset, falls back to request origin)_ | Public origin; required only when behind a proxy that rewrites Host |
| `ENRICHMENT_PROVIDERS` | `google_books,open_library,amazon,duckduckgo` | First-boot seed for provider toggles; DB is authoritative after |
| `OIDC_ISSUER_URL` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | _(unset)_ | Seeds the **generic** OIDC row on first boot; admin edits live in UI afterwards |
| `MIGRATE_ON_START` | `true` | Apply app schema + River migrations on boot |
| `OTEL_ENABLED` | `false` | Emit server traces/metrics/logs via OTLP |

## What's next

See [docs/prd.md § 11 — Planned](docs/prd.md) for the full greenfield
backlog. Near-term candidates:

- **Comic + audiobook readers** — ingest + catalog are format-agnostic;
  the missing piece is a CBZ/M4B viewer.
- **Calibre library import** — one-shot path for users migrating from
  an existing Calibre tree.
- **Forward auth / reverse-proxy header auth** — complements OIDC for
  setups that terminate SSO at the proxy (Authelia, oauth2-proxy).
- **Per-library permissions** — the user model is currently binary
  (admin / user); a shared instance with mixed audiences needs
  finer-grained library ACLs.
