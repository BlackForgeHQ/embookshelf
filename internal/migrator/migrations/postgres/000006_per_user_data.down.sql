DROP INDEX IF EXISTS idx_shelves_user_slug;
ALTER TABLE shelves DROP COLUMN IF EXISTS user_id;
ALTER TABLE shelves ADD CONSTRAINT shelves_slug_key UNIQUE (slug);

ALTER TABLE books ADD COLUMN IF NOT EXISTS progress INTEGER NOT NULL DEFAULT 0;

DROP INDEX IF EXISTS idx_ubp_book;
DROP TABLE IF EXISTS user_book_progress;
