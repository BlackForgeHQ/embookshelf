---
paths:
  - "internal/migrator/migrations/**"
  - "internal/repo/**"
  - "internal/db/**"
---

# Database & Migrations

- **Dual-dialect**: every migration must exist in BOTH `internal/migrator/migrations/postgres/` and `internal/migrator/migrations/sqlite/` with matching version prefixes. Adding only one tree breaks the other dialect at boot.
- **Never modify an existing migration.** Add a new one. Existing migrations have already run in production / on user databases.
- Every migration must be reversible. Implement both `up` and `down`.
- Test migrations in both directions, against both Postgres and SQLite, before committing.
- Migration filenames are timestamp/version-prefixed and ordered. New migrations go at the end.
- All SQL in `internal/repo/` is hand-written (no ORM). Use parameterized queries via pgx (Postgres) or `database/sql` (SQLite).
- Dialect-aware queries route through `internal/db.SelectQ(dialect, pgSQL, sqliteSQL)`. Never branch on dialect inside business logic — push the split down to the repo or the helper.
- Never seed production data in migration files. Use `scripts/seed.sql`.
- Never drop columns or tables without first confirming the data is no longer needed.
- Add indexes in their own migration, not bundled with schema changes. Easier to roll back independently.
