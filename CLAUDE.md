# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

embookshelf is a self-hosted, multi-user digital library. Go backend (Gin) + React SPA (TanStack Start) + PostgreSQL **or** SQLite. Ships as a single binary with the UI embedded via `//go:embed`. See `docs/ARCHITECTURE.md` for the full technical deep-dive and `docs/prd.md` for product intent.

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
go test ./internal/fileproc/ -run TestEPUB

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
- `internal/` — all Go backend code (27 packages, tiered)
  - **Core**: `handler/` (Gin handlers; `router.go` + SPA fallback), `service/` (~20 services: auth, library, bookdrop, enrichment, device, shelf, oidc, search, annotation, stats, reading_session, plus internal helpers metadata_writer/lock_merge/placer/scan_import), `repo/` (hand-written SQL via pgx + database/sql, dialect-aware), `model/`, `migrator/migrations/{postgres,sqlite}/`
  - **IO**: `storage/{local,s3,storagetest}/` (backend-agnostic blob interface w/ capability bits), `storageloader/` (boot-time backend resolver), `coverstore/` (SHA-256-deduped cover store), `fileproc/` (EPUB/PDF/CBZ/audio extract + EPUB/PDF embed), `extractor/` (format-agnostic façade), `sidecar/` (OPF/JSON write-back, ADR-0001), `scan/` (walker + classifier + differ + reattach), `ingest/` (BookDrop watcher), `layout/` (filename sanitization), `tagging/`, `hashing/`
  - **Cross-cutting**: `auth/` (session + bcrypt + middleware + Basic + CSRF), `config/`, `crypto/` (AES-256-GCM at rest, ADR-0010), `db/` (dialect detection + scan helpers + custom SQLite driver w/ FTS5), `queue/` (River for Postgres, polling worker for SQLite — one `Client` interface), `task/` (BookDrop / LibraryScan / ScanImport workers), `search/` (FTS5 escape helper), `sse/`, `opds/`, `provider/` (Google Books, Open Library, Hardcover, Goodreads, Amazon, DuckDuckGo + catalog + resilient client + scoring), `staticfs/dist/` (embedded SPA), `telemetry/` (OTLP)
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
- **No ORM**: all SQL is hand-written, dialect-aware via `internal/db.SelectQ(d, pgSQL, sqliteSQL)`.
- **Dual queue**: River for Postgres (4 workers), polling goroutine for SQLite — one `queue.Client` interface, dialect picks impl.
- **Pluggable storage**: every read/write of book bytes goes through `storage.Storage` (`local` or `s3`). Per-library backend in `storage_backends` table; presign vs stream gated by `EMBOOKSHELF_PRESIGN_FALLBACK`. SQLite + S3 refused at boot.
- **Streaming metadata enrichment**: `POST /books/:id/enrich/stream` returns SSE frames (`match`, `provider-error`, `done`); client disconnect cancels in-flight HTTP via context propagation.
- **Sidecar write-back (ADR-0001)**: every metadata edit lands in DB + JSON sidecar + (when supported) embedded in the file itself. `scan/reattach.go` reads sidecars on rescan to preserve user edits across renames.
- **SSE for realtime**: `/events` endpoint pushes cache invalidation events; `useRealtime()` hook integrates with TanStack Query.
- **SPA embed flow**: Vite builds to `ui/dist/` → `sync-dist.ts` copies to `internal/staticfs/dist/` → Go embeds via `//go:embed all:dist`. The Go `NoRoute` handler serves `index.html` for client-side routing.
- **Migrations are embedded**: parallel SQL trees under `migrations/postgres/` and `migrations/sqlite/`; `MIGRATE_ON_START=true` (default) auto-applies on boot per dialect.
- **Encrypted secrets**: provider API keys + cookies + OIDC client secrets stored with AES-256-GCM when `EMBOOKSHELF_SECRET_KEY` is set (ADR-0010); plaintext otherwise with a boot-time warning.
- **OIDC**: multi-provider via `user_identities` (ADR-0007); admin config DB-backed under `/api/v1/settings/oidc`, not env. First-login users land in `user_approval_status=pending` until an admin approves.

### Dev proxy

In development, Vite on `:5173` proxies `/api`, `/opds`, `/events`, `/v1` to Go on `:6060`. Open `http://localhost:5173` for the dev experience.

### Tech stack

| Layer | Stack |
|-------|-------|
| Backend | Go 1.25, Gin, pgx/v5 (Postgres) + modernc.org/sqlite, River (Postgres queue) + polling worker (SQLite), golang-migrate, AWS SDK v2 (S3), OpenTelemetry |
| Frontend | React 19, TanStack Start/Router/Query, Vite 8, Tailwind 4, shadcn/ui, Bun |
| Database | PostgreSQL 16+ (multi-user) or SQLite (single-user, default) |
| Storage | Local FS or S3-compatible (per library); presign or stream |
| Container | Multi-stage Dockerfile → distroless/nonroot |
| CI | GitHub Actions (lint, test, build, e2e, release-please) |
