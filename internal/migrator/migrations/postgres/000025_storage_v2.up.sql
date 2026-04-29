-- 000025_storage_v2.up.sql (PG)
-- Plan B of 8: backend-agnostic storage identity. Additive-only;
-- legacy books.path / libraries.path are kept and read-mirrored by
-- the Go-level backfill helper (internal/migrator/backfill_storage_v2.go).
-- Plan B2 drops them once API consumers cut over.

CREATE TABLE IF NOT EXISTS storage_backends (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL CHECK (kind IN ('local', 's3')),
    config      JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS backend_id  UUID REFERENCES storage_backends(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS root        TEXT,
    ADD COLUMN IF NOT EXISTS org_mode    TEXT NOT NULL DEFAULT 'book_per_folder'
        CHECK (org_mode IN ('book_per_file', 'book_per_folder'));

CREATE TABLE IF NOT EXISTS files (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id       UUID REFERENCES books(id) ON DELETE CASCADE,
    location      TEXT NOT NULL,
    size          BIGINT NOT NULL DEFAULT 0,
    mtime         TIMESTAMPTZ NOT NULL DEFAULT now(),
    etag          TEXT,
    content_hash  BYTEA,
    format        TEXT NOT NULL,
    last_scanned  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(library_id, location)
);

CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_files_book ON files(book_id);
CREATE INDEX IF NOT EXISTS idx_files_library ON files(library_id);

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS uuid         UUID UNIQUE,
    ADD COLUMN IF NOT EXISTS folder_path  TEXT;

ALTER TABLE bookdrop_items
    ADD COLUMN IF NOT EXISTS content_hash BYTEA;
