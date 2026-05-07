DROP INDEX IF EXISTS idx_shelves_public_slug;
ALTER TABLE shelves DROP CONSTRAINT IF EXISTS shelves_public_not_smart;
ALTER TABLE shelves DROP COLUMN IF EXISTS is_public;
