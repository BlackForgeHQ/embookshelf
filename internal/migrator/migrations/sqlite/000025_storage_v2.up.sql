-- 000025_storage_v2.up.sql (SQLite)
-- Plan B of 8: backend-agnostic storage identity. Additive-only;
-- legacy books.path / libraries.path are kept and read-mirrored by
-- the Go-level backfill helper (internal/migrator/backfill_storage_v2.go).
-- Plan B2 drops them once API consumers cut over.
--
-- SQLite type translations applied:
--   UUID PRIMARY KEY DEFAULT gen_random_uuid() → TEXT PRIMARY KEY NOT NULL  (ID supplied by app)
--   UUID (FK)                                  → TEXT
--   BYTEA                                      → BLOB
--   JSONB NOT NULL                             → TEXT NOT NULL CHECK (json_valid(col))
--   TIMESTAMPTZ NOT NULL DEFAULT now()         → TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
--   BIGINT                                     → INTEGER
--
-- SQLite limitations:
--   - "ADD COLUMN IF NOT EXISTS" is not supported by modernc.org/sqlite; plain form used.
--   - "ADD COLUMN ... UNIQUE" is not supported; uniqueness is enforced via a separate index.

CREATE TABLE IF NOT EXISTS storage_backends (
    id          TEXT PRIMARY KEY NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('local', 's3')),
    config      TEXT NOT NULL CHECK (json_valid(config)),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

ALTER TABLE libraries ADD COLUMN backend_id  TEXT REFERENCES storage_backends(id) ON DELETE RESTRICT;
ALTER TABLE libraries ADD COLUMN root        TEXT;
ALTER TABLE libraries ADD COLUMN org_mode    TEXT NOT NULL DEFAULT 'book_per_folder'
    CHECK (org_mode IN ('book_per_file', 'book_per_folder'));

CREATE TABLE IF NOT EXISTS files (
    id            TEXT PRIMARY KEY NOT NULL,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id       TEXT REFERENCES books(id) ON DELETE CASCADE,
    location      TEXT NOT NULL,
    size          INTEGER NOT NULL DEFAULT 0,
    mtime         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    etag          TEXT,
    content_hash  BLOB,
    format        TEXT NOT NULL,
    last_scanned  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE(library_id, location)
);

CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_files_book ON files(book_id);
CREATE INDEX IF NOT EXISTS idx_files_library ON files(library_id);

-- SQLite does not allow ADD COLUMN ... UNIQUE; enforce uniqueness via a partial index.
ALTER TABLE books ADD COLUMN uuid        TEXT;
ALTER TABLE books ADD COLUMN folder_path TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS books_uuid_key ON books(uuid) WHERE uuid IS NOT NULL;

ALTER TABLE bookdrop_items ADD COLUMN content_hash BLOB;
