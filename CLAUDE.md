# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

embookshelf is a self-hosted, multi-user digital library. Go backend (Gin) + React SPA (TanStack Start) + PostgreSQL. Ships as a single binary with the UI embedded via `//go:embed`. See `docs/architecture.md` for the full technical deep-dive and `docs/prd.md` for product intent.

## Commands

```bash
# First-time setup
make db-up              # start Postgres (compose.dev.yml)
make ui-install         # bun install in ui/
make seed               # dev seed: admin@local / changeme

# Development
make up                 # backend (air live-reload on :6060) + Vite (:5173) together
make dev                # backend only (go tool air)
make ui-dev             # Vite only (proxies /api, /opds, /events, /v1 to :6060)

# Build
make build              # ui-build + go build → ./tmp/embookshelf
make ui-build           # Vite build + sync dist into internal/staticfs/dist/

# Test & lint
make test               # go test ./...
make go-lint            # golangci-lint v2.11.4
make ui-lint            # ESLint (cd ui && bun run lint)
make ui-typecheck       # tsc --noEmit (cd ui && bun run typecheck)
make ui-test            # vitest run (cd ui && bun run test)
make ci-local           # all CI checks in parallel

# Single Go test
go test ./internal/pattern/ -run TestResolve

# Migrations
make migrate            # apply pending (go run ./cmd/migrate up)
make migrate-down       # revert latest
make migrate-version    # show current version

# E2E (Playwright)
make e2e-install        # npm + chromium in e2e/
make e2e                # run against live dev stack (needs `make up`)
make e2e-ui             # interactive mode

# Docker
make docker-build       # single-arch → embookshelf:dev
make docker-run         # run :dev against local Postgres
```

## Architecture

### Monorepo layout

- `cmd/embookshelf/` — Go entry point, service wiring
- `cmd/migrate/` — migration CLI (up/down/version)
- `internal/` — all Go backend code
  - `handler/` — Gin HTTP handlers; `router.go` wires routes + SPA fallback
  - `service/` — business logic (auth, library, bookdrop, enrichment, device, shelf)
  - `repo/` — data access layer (raw pgx, no ORM, hand-written SQL)
  - `queue/` — River job queue wrapper (PostgreSQL-backed)
  - `task/` — River worker implementations (bookdrop ingest, library scan)
  - `provider/` — metadata enrichment (Google Books, Open Library, Hardcover, Goodreads, Amazon, DuckDuckGo)
  - `migrator/migrations/` — golang-migrate SQL files (embedded in binary)
  - `staticfs/dist/` — embedded SPA assets (written by `ui/scripts/sync-dist.ts`)
  - `config/` — env var loading
  - `auth/` — session + OIDC/SSO
  - `ingest/` — EPUB/PDF metadata extraction
  - `pattern/` — file naming pattern resolver
  - `sse/` — server-sent events
  - `coverstore/` — cover image storage
  - `crypto/` — AES-256-GCM encryption for secrets at rest
- `ui/` — TanStack Start SPA (React 19, Vite 8, Tailwind 4, shadcn/ui, Bun)
  - `src/routes/` — file-based TanStack Router pages
  - `src/api/` — typed API client functions
  - `src/components/` — React components; `ui/` subdirectory is shadcn primitives
  - `src/hooks/` — custom hooks (e.g., `useRealtime` wires SSE → Query cache invalidation)
  - `src/styles.css` — Tailwind design tokens under `@theme`
- `e2e/` — Playwright tests (separate Node project)
- `scripts/seed.sql` — dev seed data

### Key patterns

- **Service → Repo split**: services orchestrate business logic, repos handle SQL.
- **No ORM**: all SQL is hand-written with pgx prepared statements.
- **River queue**: bookdrop ingest and library scans run as async workers (default 4).
- **Streaming metadata enrichment**: `POST /books/:id/enrich/stream` returns SSE frames (`match`, `provider-error`, `done`); client disconnect cancels in-flight HTTP via context propagation.
- **SSE for realtime**: `/events` endpoint pushes cache invalidation events; `useRealtime()` hook integrates with TanStack Query.
- **SPA embed flow**: Vite builds to `ui/dist/` → `sync-dist.ts` copies to `internal/staticfs/dist/` → Go embeds via `//go:embed all:dist`. The Go `NoRoute` handler serves `index.html` for client-side routing.
- **Migrations are embedded**: SQL files ship inside the binary; `MIGRATE_ON_START=true` (default) auto-applies on boot.
- **Encrypted secrets**: provider API keys stored with AES-256-GCM when `EMBOOKSHELF_SECRET_KEY` is set.

### Dev proxy

In development, Vite on `:5173` proxies `/api`, `/opds`, `/events`, `/v1` to Go on `:6060`. Open `http://localhost:5173` for the dev experience.

### Tech stack

| Layer | Stack |
|-------|-------|
| Backend | Go 1.25, Gin, pgx/v5, River (queue), golang-migrate |
| Frontend | React 19, TanStack Start/Router/Query, Vite 8, Tailwind 4, shadcn/ui, Bun |
| Database | PostgreSQL 16+ |
| Container | Multi-stage Dockerfile → distroless/nonroot |
| CI | GitHub Actions (lint, test, build, e2e, release-please) |
