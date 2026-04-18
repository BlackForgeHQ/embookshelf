-- Books now remember the file path that fed them (until file moves land,
-- this is the same path the bookdrop watcher saw).
ALTER TABLE books ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_books_path ON books(path) WHERE path <> '';

-- Per-user resume position inside an EPUB. Stored as an epub.js CFI string.
-- Empty means "no known position — open at the start".
ALTER TABLE user_book_progress ADD COLUMN IF NOT EXISTS resume_cfi TEXT NOT NULL DEFAULT '';

-- Backfill paths for already-approved bookdrop imports so the reader works
-- on books ingested before this migration.
UPDATE books b
SET path = bi.path
FROM bookdrop_items bi
WHERE bi.book_id = b.id AND b.path = '';
