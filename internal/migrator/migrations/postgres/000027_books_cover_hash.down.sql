DROP INDEX IF EXISTS idx_books_cover_hash;
ALTER TABLE books DROP COLUMN IF EXISTS cover_hash;
