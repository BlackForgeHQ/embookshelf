# embookshelf

Self-hosted, multi-user digital library. This repo currently contains the bootstrap skeleton for the Go + Templ + HTMX + Postgres stack described in [Architecture.md](Architecture.md).

## What's in this skeleton

- Go 1.24 server on `:6060` (Gin router, pgx/v5 pool, graceful shutdown).
- Templ views: `app` layout, dashboard + library pages, `Sidebar` / `TopBar` / `Cover` / `StatusBar` components, a `LibraryGrid` partial for HTMX swaps.
- Tailwind 4 CSS-first config with the mockup's paper/ink/accent/cover tokens.
- golang-migrate migration (paired `.up.sql` / `.down.sql`) creating `libraries` + `books`, embedded into a `cmd/migrate` CLI and available to the server at boot. sqlc.yaml is wired (not yet generating).
- Postgres dev env via `compose.dev.yml`.
- Multi-stage Dockerfile building the production binary.
- `go.mod` tool directive for `templ` — no global installs.

## First-time setup

```bash
# 1. Start Postgres
make db-up

# 2. Fetch htmx + compile Tailwind
npm install
make assets

# 3. Generate Templ code, build, and run — migrations apply on boot
#    (disable with MIGRATE_ON_START=false if you prefer to run them manually)
make run

# 4. (optional) in another shell: load seed data so the library page isn't empty
make seed
```

Migrations can still be run manually via `make migrate` / `go run ./cmd/migrate up`
when `MIGRATE_ON_START=false`.

Open http://localhost:6060/app/library.

## Dev loop

```bash
make css-watch   # terminal 1 — Tailwind watcher
make dev         # terminal 2 — air watches *.go / *.templ
```

## Next steps (not yet implemented)

Auth (JWT/OIDC/remote), SSE hub, river background jobs, file processors (PDF/EPUB/CBX/audio), metadata providers, BookDrop ingest, OPDS/Kobo sync, multi-user, readers. See PRD.md and Architecture.md for the target shape.
