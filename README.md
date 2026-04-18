# embookshelf

Self-hosted, multi-user digital library — Go + Templ + HTMX + Postgres.
See [Architecture.md](Architecture.md) for the technical shape and
[PRD.md](PRD.md) for the product intent + roadmap.

## What's built

- **Server** — Go 1.24 on `:6060` (Gin, pgx/v5 pool, graceful shutdown).
- **UI** — server-rendered Templ pages enhanced with HTMX 2 + a handful of
  ~40-line vanilla-JS shims for SSE, metadata enrichment, and the two readers.
- **Design system** — Tailwind 4 CSS-first config with a research-backed
  editorial type stack: **Source Serif 4** (UI/display) + **Literata**
  (long-form reading body, variable `opsz`) + **IBM Plex Mono** (metadata).
  Navy-tinted ink, library-burgundy accent, warm-ivory paper.
- **Auth** — local session cookies (server-side `sessions` table), Basic
  Auth for OPDS, bcrypt passwords, SameSite + Origin/Referer CSRF. First-run
  signup creates the admin.
- **Ingest** — watched `/bookdrop` folder + an admin-side library filesystem
  scanner. Both funnel through a `bookdrop_items` review queue with SSE-live
  UI updates. EPUB metadata + cover extraction today (stdlib only, no
  external dep); other formats stubbed.
- **Readers** — EPUB (epub.js) and PDF (PDF.js). Per-user progress with
  format-aware resume tokens (`epubcfi(...)` vs `page:N`) stored in
  `user_book_progress`.
- **Library UX** — full-text search (`tsvector` + GIN), sort, format filter;
  book detail, metadata editor, per-user shelves (create, delete, toggle
  membership), progress slider.
- **Metadata enrichment** — Google Books + Open Library providers, concurrent
  fan-out, confidence-sorted match cards, one-click "Use this cover"
  (allow-listed hosts only).
- **OPDS 1.2 catalog** at `/opds/*` — root nav + per-library / recent /
  search acquisition feeds + OpenSearch description. Works with KOReader,
  Moon+ Reader, FBReader, Aldiko, Marvin, etc.
- **Background jobs** — river queue, workers for `bookdrop.ingest` and
  `library.scan`. River's own migrations apply on boot.
- **Migrations** — golang-migrate with embedded SQL; app applies pending
  migrations on boot (`MIGRATE_ON_START=true` by default) or via
  `go run ./cmd/migrate up`.

## First-time setup

```bash
# 1. Start Postgres
make db-up

# 2. Fetch htmx + epub.js + pdf.js + compile Tailwind
npm install
make assets

# 3. Generate Templ code, build, and run — migrations apply on boot.
#    (set MIGRATE_ON_START=false to manage them externally.)
make run

# 4. (optional, separate shell) seed an admin + sample library
make seed
```

Open <http://localhost:6060/>. It 302s to `/app`, which 302s to `/login`.

**Dev credentials** (from `make seed`): `admin@local` / `changeme`.

To test the OPDS catalog from an e-reader app, point it at
`http://<host>:6060/opds/` with the same credentials.

## Dev loop

```bash
make css-watch   # terminal 1 — Tailwind watcher
make dev         # terminal 2 — `go tool air` rebuilds on .go / .templ / .sql
```

## Useful targets

| Target | What it does |
|--------|-------------|
| `make db-up` / `make db-down` | Start / stop Postgres via `compose.dev.yml` |
| `make migrate` / `make migrate-down` / `make migrate-version` | Manual migration ops via `go run ./cmd/migrate` |
| `make seed` | Pipe `scripts/seed.sql` into the running Postgres container |
| `make assets` | `htmx.min.js` + `epub.min.js` + `jszip.min.js` + `pdf.min.js` + Tailwind build |
| `make htmx` / `make epubjs` / `make pdfjs` / `make css` | Individual asset targets |
| `make build` / `make run` | Build or run the server (applies migrations on boot) |
| `make dev` | Live-reload loop via `go tool air` |
| `make test` | `go test ./...` |

## Dev data notes

- Covers live under `${DATA_PATH}/covers/{books,bookdrop}/{id}`. `${DATA_PATH}` defaults to `./data`.
- Bookdrop scans `${BOOKDROP_PATH}` every 5 s (configurable via
  `BOOKDROP_POLL_SECONDS`). Default path is `./bookdrop/`.
- `./data/` and `./bookdrop/` are `.gitignore`d.
- Additional library filesystem roots are configured from the
  **Settings → Libraries** page (admin only) rather than env vars.

## Roadmap highlights

See `PRD.md` § 11 for the full current-state table. The biggest gaps vs. the
PRD are OIDC + remote auth, Kobo / KOReader sync, CBX + audiobook readers,
bookmarks/highlights/annotations, and smart (rule-based) shelves.
