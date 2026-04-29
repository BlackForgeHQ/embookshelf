-- See postgres/000024_audiobook_metadata.up.sql.
--
-- SQLite type translations:
--   INTEGER (nullable)  → INTEGER (no constraint added; NULL when not set)
--   TEXT NOT NULL DEFAULT '' → TEXT NOT NULL DEFAULT ''
--   JSONB (nullable)    → TEXT CHECK (col IS NULL OR json_valid(col))

ALTER TABLE books ADD COLUMN duration_seconds INTEGER;
ALTER TABLE books ADD COLUMN narrator         TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN chapters         TEXT CHECK (chapters IS NULL OR json_valid(chapters));
