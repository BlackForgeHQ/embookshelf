CREATE TABLE IF NOT EXISTS library_paths (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id       UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path             TEXT NOT NULL,
    last_scanned_at  TIMESTAMPTZ,
    file_count       INTEGER NOT NULL DEFAULT 0,
    discovered_count INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, path)
);

CREATE INDEX IF NOT EXISTS idx_library_paths_library ON library_paths(library_id);
