ALTER TABLE books ADD COLUMN cover_hash BLOB;
CREATE INDEX IF NOT EXISTS idx_books_cover_hash ON books(cover_hash) WHERE cover_hash IS NOT NULL;
