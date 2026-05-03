# embookshelf

Self-hosted, multi-user digital library — Go backend + React SPA
(TanStack Start + shadcn/ui) + Postgres. EPUB + PDF readers, full-text
search, per-user shelves + annotations, metadata enrichment across four
providers, live BookDrop import queue, OIDC/SSO (Google / GitHub /
generic), reMarkable device sync, and an OPDS 1.2 catalog for e-readers.
Ships as a single binary with the UI embedded.

See [docs/architecture.md](docs/architecture.md) for the technical shape
and [docs/prd.md](docs/prd.md) for the product intent + roadmap.

> **2026-04 update:** SQLite is now the default backend. If you were relying on the bare-default `postgres://localhost:5432/embookshelf` connection (i.e. running without `DATABASE_URL` set), update your config to set `DATABASE_URL` explicitly to your Postgres DSN.

## Quickstart

### Single-user / self-hosted (SQLite, default)

```bash
docker run --rm -p 6060:6060 -v $(pwd)/data:/data ghcr.io/blackforgehq/embookshelf:latest
```

Open http://localhost:6060 and create your admin user. The library lives at `./data/embookshelf.db`. No external database required.

### Multi-user / production (Postgres)

For shared installs, run with an explicit `DATABASE_URL`:

```bash
docker run --rm -p 6060:6060 \
  -e DATABASE_URL='postgres://user:pass@dbhost:5432/embookshelf?sslmode=disable' \
  -v $(pwd)/data:/data \
  ghcr.io/blackforgehq/embookshelf:latest
```

The Postgres path supports concurrent writes and the full bookdrop ingest pipeline.

---

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
- **Streaming enrichment** — `POST /books/:id/enrich/stream` is a
  Server-Sent Events endpoint; each provider frame arrives as it
  finishes so the match list fills in as providers return. Frames are
  `event: match`, `event: provider-error`, `event: done`; client
  disconnects cancel every in-flight outbound HTTP call via context
  propagation.
- **Per-field locks** — padlock toggles next to title, subtitle,
  author, description, publisher, series, ISBN-13/10, language,
  publish date, pages, genres, moods, tags, and cover. Locked fields
  are skipped by the apply-metadata flow (`PUT /books/:id/metadata`)
  and auto-enrich. Manual `PATCH` still writes — the lock is against
  automation, not user intent.
- **Six providers** — Google Books (API), Open Library (API),
  Hardcover (GraphQL; requires token), Goodreads (HTML scrape, polite
  with 60 s cooldown on 429/403), Amazon (ISBN-10 cover fallback only),
  DuckDuckGo (Wikipedia summary). Confidence scoring uses a fuzzy
  Levenshtein tier so slight title/punctuation drift still sorts
  sensibly. Results are cached in-process for 5 min; a per-provider
  60 s cooldown kicks in on 429 so repeated refetches don't burn rate
  limits.
- **Provider config + priority** — per-provider JSONB config
  (API keys, language, cookie; stored encrypted when a KEK is set),
  enable/disable, and priority ordering. Priority drives the ISBN
  lookup chain: `POST /books/metadata/isbn-lookup` walks providers
  in rank order, first hit wins.
- **Apply flow** — streaming search returns candidate cards; each
  card has `Apply` (atomic server-side write, respects locks, optional
  cover), `Use fields` (populate form for review), and `Use cover`
  (cover-only).
- **Auto-enrich on bookdrop approve** — opt-in toggle in
  `Settings → Metadata providers`. On approval, the service runs the
  ISBN chain (falling back to fan-out at Confidence ≥ 70) and writes
  only fields empty on the extracted book, respecting existing locks.
- **Provider health** — every `Search` call records last-success /
  last-error timestamps + error string. Each row in the settings
  panel shows a badge like `ok 12m ago` or `failed 2m ago — hardcover
  401` so stale tokens don't silently rot.
- **Secrets at rest** — password-kind config fields (API keys,
  tokens) are encrypted with AES-256-GCM before hitting
  `provider_settings.config`. KEK comes from
  `EMBOOKSHELF_SECRET_KEY` (base64-encoded 32 bytes); unset = log a
  warning and round-trip plaintext. Pre-encryption rows pass through
  on read; next write upgrades them.
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
- **Metadata providers** — enable/disable each provider, reorder
  priority with up/down arrows, fill provider-specific config
  (Google Books API key + language, Hardcover token, …), toggle
  auto-enrich on bookdrop approve, and see per-provider health
  badges. Google Books / Open Library / Amazon / DuckDuckGo are
  enabled by default on fresh installs; Hardcover + Goodreads land
  disabled (the former needs a token, the latter is scrape-only and
  brittle).
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
| `DATABASE_URL` | `sqlite://./data/embookshelf.db` | Database connection. SQLite by default; set to a `postgres://…` DSN to use Postgres instead |
| `EMBOOKSHELF_PORT` | `6060` | HTTP listen port |
| `ALLOWED_ORIGINS` | `*` | CSRF allow-list for `Origin`/`Referer` |
| `SESSION_SECRET` | _(empty — dev only)_ | Sign session cookies; set in prod |
| `BOOKDROP_PATH` | `./bookdrop` | Watched folder for imports |
| `DATA_PATH` | `./data` | Covers + on-disk caches |
| `APP_URL` | _(unset, falls back to request origin)_ | Public origin; required only when behind a proxy that rewrites Host |
| `EMBOOKSHELF_SECRET_KEY` | _(unset — dev only)_ | Base64-encoded 32 bytes (`openssl rand -base64 32`). Encrypts provider API keys / tokens at rest. Unset = plaintext storage + loud boot warning |
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
