CREATE TABLE IF NOT EXISTS bookdrop_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    path          TEXT NOT NULL UNIQUE,           -- absolute path on disk
    file_size     BIGINT NOT NULL DEFAULT 0,
    format        TEXT NOT NULL DEFAULT '',       -- guessed from extension
    state         TEXT NOT NULL DEFAULT 'discovered'
                  CHECK (state IN ('discovered','processing','ready','failed','imported','rejected')),
    progress      INTEGER NOT NULL DEFAULT 0,
    error_msg     TEXT NOT NULL DEFAULT '',

    -- Extracted metadata (filled by the fileproc worker).
    title         TEXT NOT NULL DEFAULT '',
    author        TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    language      TEXT NOT NULL DEFAULT '',
    has_cover     BOOLEAN NOT NULL DEFAULT false,

    -- Set when approved and a book row is created.
    book_id       UUID REFERENCES books(id) ON DELETE SET NULL,

    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_bookdrop_state ON bookdrop_items(state);
CREATE INDEX IF NOT EXISTS idx_bookdrop_discovered_at ON bookdrop_items(discovered_at DESC);
