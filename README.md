# embookshelf

Self-hosted, multi-user digital library — Go backend + React SPA
(TanStack Start) + Postgres.

> **In-flight refactor.** The frontend is being rebuilt as a TanStack Start
> SPA (SPA mode, no SSR). The old Templ/HTMX UI and most `/app/*` routes
> are gone; JSON endpoints are being added under `/api/v1/*`. Only OPDS
> (`/opds/*`) and the healthcheck work end-to-end today.

See [docs/architecture.md](docs/architecture.md) and [docs/prd.md](docs/prd.md).

## Layout

```
cmd/                     Go entry points
internal/                Go backend (auth, service, repo, handler, …)
internal/staticfs/dist/  Embedded SPA shell (synced by frontend build)
frontend/                TanStack Start app (Vite + TS + Tailwind v4)
docs/                    PRD + architecture
```

## Development

```bash
# 1. Start Postgres
make db-up

# 2. Install frontend deps
make frontend-install

# 3. Terminal A — Go backend on :6060
make dev

# 4. Terminal B — Vite dev server on :5173 (proxies /api, /opds, /events → :6060)
make frontend-dev
```

Open <http://localhost:5173/>.

## Production build

```bash
make build   # frontend build + SPA sync → internal/staticfs/dist, then go build
./tmp/embookshelf
```

The binary ships self-contained — the SPA shell is embedded via
`//go:embed all:dist` in [internal/staticfs/staticfs.go](internal/staticfs/staticfs.go).

## How the SPA is served

TanStack Start is configured in **SPA mode** (see [frontend/vite.config.ts](frontend/vite.config.ts))
so the build emits a static shell + client bundle — no Node runtime needed
in production. The Go `NoRoute` handler in
[internal/handler/router.go](internal/handler/router.go) serves the shell
for any unmatched GET so client-side routing works on hard reloads.

Tailwind v4 is compiled via the standalone CLI into `src/generated.css`
(see the `css` / `css:watch` npm scripts) rather than through the Vite
plugin, because `@tailwindcss/node`'s ESM loader conflicts with Start's
prerender pass.

## OPDS

Catalog at `/opds/*` with HTTP Basic Auth. Works with KOReader, Moon+ Reader,
FBReader, Aldiko, Marvin, etc. Seed admin: `admin@local` / `changeme`
(via `make seed`).

## What's next

- JSON endpoints under `/api/v1/*` replacing the deleted Templ handlers
  (auth, library, book, bookdrop, enrichment, reader, settings).
- SSE stream (`/events`) for bookdrop queue updates.
- Port the reader experience (EPUB.js, PDF.js) into React components.
