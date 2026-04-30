-- See postgres/000029_bookdrop_audio.up.sql.
--
-- SQLite type translations:
--   INTEGER (nullable)  → INTEGER (no constraint added; NULL when not set)
--   TEXT NOT NULL DEFAULT '' → TEXT NOT NULL DEFAULT ''
--   JSONB (nullable)    → TEXT CHECK (col IS NULL OR json_valid(col))

ALTER TABLE bookdrop_items ADD COLUMN duration_seconds INTEGER;
ALTER TABLE bookdrop_items ADD COLUMN narrator         TEXT NOT NULL DEFAULT '';
ALTER TABLE bookdrop_items ADD COLUMN chapters         TEXT CHECK (chapters IS NULL OR json_valid(chapters));
