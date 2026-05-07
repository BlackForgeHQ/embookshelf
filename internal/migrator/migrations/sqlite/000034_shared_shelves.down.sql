DROP INDEX IF EXISTS idx_shelves_public_slug;
ALTER TABLE shelves DROP COLUMN is_public;
