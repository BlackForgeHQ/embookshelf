# CLAUDE.md

embookshelf: Go (Gin) + React 19 (TanStack Start) + Postgres **or** SQLite, single binary with embedded UI. See `docs/ARCHITECTURE.md` and `docs/prd.md`.

## Commands

```bash
make up               # backend (:6060) + Vite (:5173)
make build            # ui-build + go build → ./tmp/embookshelf
make test             # go test ./...
make ci-local         # all CI checks in parallel
make go-lint          # golangci-lint v2.11.4
make ui-lint          # ESLint (cd ui && bun run lint)
make ui-typecheck     # tsc --noEmit
make ui-test          # vitest run
make migrate          # apply migrations
go test ./internal/fileproc/ -run TestEPUB   # single Go test
make e2e              # Playwright (needs `make up`)
```

## Key decisions

- **Dual dialect, no ORM**: every SQL is hand-written; pair Postgres + SQLite trees in `internal/migrator/migrations/{postgres,sqlite}/` and route via `internal/db.SelectQ(d, pgSQL, sqliteSQL)`. Never add a Postgres-only migration without its SQLite sibling.
- **Sidecar write-back (ADR-0001)**: metadata edits go to DB + JSON sidecar + (when supported) embedded in the file. `scan/reattach.go` reads sidecars on rescan to preserve user edits across renames.
- **Pluggable storage**: book bytes always flow through `storage.Storage`. SQLite + S3 is refused at boot.
- **Encrypted secrets (ADR-0010)**: provider API keys, cookies, OIDC client secrets use AES-256-GCM when `EMBOOKSHELF_SECRET_KEY` is set.
- **SPA embed**: Vite → `ui/dist/` → `sync-dist.ts` copies to `internal/staticfs/dist/` → `//go:embed all:dist`. Both `internal/staticfs/dist/` and `ui/src/routeTree.gen.ts` are generated — don't edit by hand.

## Agent skills

Issue tracker, triage labels, domain docs configured per `docs/agents/{issue-tracker,triage-labels,domain}.md`. GitHub Issues via `gh`; default canonical triage labels; single-context `CONTEXT.md` + `docs/adr/`.
