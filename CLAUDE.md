# CLAUDE.md

embookshelf: Go (Gin) + React 19 (TanStack Start) + Postgres, single binary with embedded UI. See `docs/architecture.md` and `docs/PRD.md`.

## Commands

```bash
make up               # backend (:6060) + Vite (:5173)
make build            # ui-build + go build → ./tmp/embookshelf
make test             # go test ./...
make ci-local         # all CI checks in parallel
make go-lint          # golangci-lint (version pinned in Makefile)
make ui-lint          # Biome lint (cd ui && bun run lint)
make ui-typecheck     # tsc --noEmit
make ui-test          # vitest run
make migrate          # apply migrations
go test ./internal/fileproc/ -run TestEPUB   # single Go test
make e2e              # Playwright (needs `make up`)
```

## Key decisions

- **Postgres only, no ORM** (ADR-0023): embookshelf is a Postgres application. SQL stays hand-written; no ORM. The dual-dialect machinery is gone: `db.SelectQ`, `db.ValueStringSlice`, FTS5 search and the polling queue are deleted, and `db.ScanTime`/`ScanNullTime`/`ScanStringSlice` are Postgres-only (no dialect argument). Don't add new dialect branches or SQLite migrations; write Postgres-only. What survives for the importer only: `db.DetectDialect`, the SQLite open path, `internal/migrator/migrations/sqlite/`, and `internal/sqliteimport/`. Existing installs migrate via `embookshelf import-sqlite --from <file.db>`, the one remaining reason the binary links a SQLite driver.
- **Sidecar write-back (ADR-0001)**: metadata edits go to DB + JSON sidecar + (when supported) embedded in the file. `scan/reattach.go` reads sidecars on rescan to preserve user edits across renames.
- **Pluggable storage**: book bytes always flow through `storage.Storage`. Backends are loaded from `storage_backends` rows at boot by `internal/storageloader`.
- **Encrypted secrets (ADR-0010)**: provider API keys, cookies, OIDC client secrets use AES-256-GCM when `EMBOOKSHELF_SECRET_KEY` is set.
- **SPA embed**: Vite → `ui/dist/` → `sync-dist.ts` copies to `internal/staticfs/dist/` → `//go:embed all:dist`. Both `internal/staticfs/dist/` and `ui/src/routeTree.gen.ts` are generated — don't edit by hand.

## Agent skills

Issue tracker, triage labels, domain docs configured per `docs/agents/{issue-tracker,triage-labels,domain}.md`. GitHub Issues via `gh`; default canonical triage labels; single-context `CONTEXT.md` + `docs/adr/`.
