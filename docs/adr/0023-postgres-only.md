# ADR-0023: Postgres only — drop SQLite support

- Status: Accepted (2026-07-26)
- Deciders: Bohdan Shaparenko (@shbodya)
- Supersedes: the dual-dialect half of ADR-0004-era assumptions; reverses
  the "SQLite is the default backend" position taken 2026-04 and the
  "sqlc-staged over ORM" line in `docs/ARCHITECTURE.md`

## Context

embookshelf supports two databases. Every SQL statement is hand-written
twice, and the cost has compounded well past what the dual-dialect
decision anticipated:

- **82** `DialectSQLite` branches and **103** `db.SelectQ` call sites
  across ~24 repo files. Two competing idioms for the same concern —
  `SelectQ` pairs and hand-rolled `if dialect ==` blocks — coexist in
  nearly every file.
- **32** SQLite migration files that must be kept in lockstep with their
  Postgres siblings, by convention only.
- Parallel implementations wherever the two engines diverge: FTS5 vs
  `tsvector` search, a polling job queue vs River, `ValueStringSlice` /
  `ScanStringSlice` / `ScanTime` value conversions, and a
  hand-maintained `dberr.sqliteUniqueIndex` map whose own comment asks
  future readers to "keep this in sync."

The failure mode is not theoretical. Postgres uses `$N` placeholders
bound by label; SQLite uses `?` bound by position. One text can be
correct while its sibling is silently wrong, and only one dialect is
exercised. Commit `909f6bf` fixed exactly this in five `UserRepo`
methods. `BookRepo.UpdateMetadata` still carries 39 columns as two SQL
texts plus two copy-pasted argument lists, and `UserRepo.TouchLastSeen`
deliberately numbers `$2` before `$1` so its argument order can differ
from the SQLite arm — correct today, and a trap for anyone who "tidies"
it.

Running the suite against real Postgres surfaced three tests that pass on
SQLite and fail on Postgres, all pre-dating this decision:

- `TestLibraryRepo_setCoverHash` and `TestPendingOrphanRepo_RoundTrip` —
  feed non-UUID strings into columns that are `TEXT` on SQLite and
  `uuid` on Postgres.
- `TestBackfillStorageV2_OneLibraryOneBook` — "inconsistent types deduced
  for parameter $1" (SQLSTATE 42P08), a Postgres-only inference error.

The dual-dialect matrix exists (`make test-pg`) but the default `make
test` runs SQLite only, so this drift was invisible. Three tests silently
covering one dialect is the same failure mode as `909f6bf` at the suite
level.

Building the importer surfaced a second, sharper instance: SQLite ships
with `PRAGMA foreign_keys` **off**, so a long-lived SQLite database can
hold rows whose parent no longer exists. Postgres rejects them. Two
engines were not merely spelling SQL differently — they were enforcing
different invariants on the same data.

Meanwhile the features that matter most to the product — `jsonb`,
`tsvector` full-text search, River's transactional job queue, multi-user
concurrent writes — are all Postgres-only. SQLite installs get degraded
substitutes of each.

## Decision

**embookshelf is a Postgres application.** SQLite support is removed:
the dual-dialect machinery, the FTS5 search engine, and the polling queue
backend all go. `DATABASE_URL` must name a Postgres DSN; a `sqlite://`
DSN refuses to boot with a message pointing at the importer.

Two SQLite artifacts deliberately survive, both scoped to the importer
and both marked for deletion with it: the `modernc.org/sqlite` driver
registration, and the SQLite **migration tree**. The tree has to stay
because an operator can upgrade from an old release directly to this one,
leaving a source database at an older schema that must be brought forward
before its rows map onto the current Postgres schema. A CI job
(`migrations-sanity-sqlite-importer`) keeps that tree honest.

Existing SQLite installs get a **one-shot importer** shipped as a
subcommand of the same binary:

```
embookshelf import-sqlite --from ./data/embookshelf.db
```

It reads the SQLite file and writes a migrated Postgres database,
translating the two encodings that genuinely differ: JSON-text arrays
become `text[]`, and RFC3339 TEXT timestamps become `timestamptz`. This
is the reason `modernc.org/sqlite` stays in the dependency tree — a
read-only path for the importer only, deletable once the deprecation
window closes.

Rollout is two releases so no operator is ever stranded:

- **Release A** — importer lands while SQLite still works. Booting on a
  `sqlite://` DSN logs a deprecation warning naming the command.
- **Release B** — dual-dialect code deleted; `sqlite://` refuses to boot.

## Consequences

Positive:

- The largest single simplification available in this codebase. The
  placeholder-drift bug class stops existing rather than being tested
  for, and one query text replaces every pair.
- One search engine, one queue backend, one migration tree, one set of
  value conversions. `SelectQ`, `ValueStringSlice`, `ScanStringSlice`,
  `ScanTime`, and the `sqliteUniqueIndex` map all become unnecessary.
- `jsonb`, `tsvector`, and River stop being a premium tier. Every
  install gets the good implementation.
- No ORM needed. The dialect problem was the main argument for adopting
  one (bun was evaluated); single-dialect hand-written SQL with pgx is
  already fine, so the "no ORM" decision survives on its merits rather
  than by inertia.

Negative:

- **Every self-hoster now needs Postgres.** This is the real cost: the
  zero-dependency single-binary-plus-file install goes away, which is
  precisely what the 2026-04 SQLite-default change was for. It narrows
  who the software is for, deliberately.
- Existing SQLite users must run a migration step. The importer reduces
  this to one command, but it is still a manual step with a failure mode
  (partially imported target) that documentation has to cover.
- Local development and `make test` now require a running Postgres. All
  nine repo tests currently default to SQLite via `repotest.New(t)`; the
  Makefile takes on starting the `compose.dev.yml` service so tests
  actually run rather than quietly skipping.
- `modernc.org/sqlite` and the SQLite migration tree stay for the
  importer, so "no SQLite" is true of the runtime, not of the repository.
  The 32 migration files are dead weight for everyone who never had a
  SQLite install.

Neutral:

- `repotest`'s dialect-matrix harness collapses to a single backend; the
  `REPOTEST_DIALECT` env var and `NewWithDialect` lose their reason to
  exist.
- The three pre-existing Postgres test failures must be fixed as part of
  Release B — with SQLite gone they are no longer "one dialect passes."

## Alternatives considered

**Keep both dialects; add `db.Rebind` plus a time-value helper (~150
lines).** Rewrites one `?`-placeholder query text into `$N` for
Postgres, making mis-numbering unrepresentable. This was the cheapest
fix for the specific bug class and reversed no decisions. Rejected
because it addresses only the placeholder axis: two migration trees, two
search engines, two queue backends, and the value-conversion helpers all
survive. It treats the symptom.

**Adopt bun (`uptrace/bun`) as a dialect-aware query layer.** bun uses
`?` everywhere and rewrites per dialect, wraps an existing `*sql.DB` for
incremental adoption, and keeps SQL hand-written via `NewRaw`. Genuinely
a good fit for a dual-dialect codebase — a better one than the staged
sqlc plan, since sqlc generates per engine and would have formalized the
duplication rather than removing it. Rejected because it buys a
dependency and a new idiom to solve a problem we are choosing to delete
instead; bun's array handling is Postgres-only anyway, so the
`[]string` conversions would have survived it.

**Manual migration path (pgloader, `sqlite3 .dump`).** Rejected for the
importer. This schema is exactly where generic tools break: JSON-text
arrays must become `text[]` and RFC3339 TEXT must become `timestamptz`.
Getting that wrong is silent data corruption in a user's library.
