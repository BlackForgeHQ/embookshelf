-- Shared shelves. See postgres/000034_shared_shelves.up.sql for the
-- full rationale. SQLite doesn't support adding a CHECK to an
-- existing table without rebuild, so the constraint lives on the
-- partial unique index expression below — slug uniqueness only kicks
-- in when is_public = 1, and the legitimacy of "is_public = 1 AND
-- is_smart = 1" is enforced application-side at publish time.
ALTER TABLE shelves
    ADD COLUMN is_public INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_shelves_public_slug
    ON shelves(slug) WHERE is_public = 1;
