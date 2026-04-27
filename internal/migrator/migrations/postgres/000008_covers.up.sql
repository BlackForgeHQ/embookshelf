-- Books remember whether a cover image is stored on disk and, if so, its
-- MIME type. The file itself lives under ${DATA_PATH}/covers/books/{id}.
ALTER TABLE books
    ADD COLUMN IF NOT EXISTS has_cover  BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS cover_mime TEXT    NOT NULL DEFAULT '';

-- Bookdrop items already track has_cover (added in 000005) — keep the MIME
-- type alongside so the queue UI can serve a preview without guessing.
ALTER TABLE bookdrop_items
    ADD COLUMN IF NOT EXISTS cover_mime TEXT NOT NULL DEFAULT '';
