CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS libraries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS books (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    title          TEXT NOT NULL,
    author         TEXT NOT NULL DEFAULT '',
    format         TEXT NOT NULL DEFAULT 'EPUB',
    year           INTEGER NOT NULL DEFAULT 0,
    progress       INTEGER NOT NULL DEFAULT 0,
    rating         INTEGER NOT NULL DEFAULT 0,
    cover_palette  TEXT NOT NULL DEFAULT 'navy',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_books_library_id ON books(library_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_books_title ON books(title) WHERE deleted_at IS NULL;
