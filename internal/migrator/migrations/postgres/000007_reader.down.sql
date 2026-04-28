ALTER TABLE user_book_progress DROP COLUMN IF EXISTS resume_cfi;
DROP INDEX IF EXISTS idx_books_path;
ALTER TABLE books DROP COLUMN IF EXISTS path;
