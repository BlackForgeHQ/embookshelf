-- Shared shelves: an admin-curated, instance-wide shelf surfaced as a
-- read-only sidebar entry for every user. One row, many viewers — the
-- owning admin's edits propagate without per-user clones.
--
-- Smart shelves cannot be shared because their rule predicates touch
-- per-user fields (rating, progress) that don't translate cross-user.
ALTER TABLE shelves
    ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE shelves DROP CONSTRAINT IF EXISTS shelves_public_not_smart;
ALTER TABLE shelves
    ADD CONSTRAINT shelves_public_not_smart CHECK (
        is_public = false OR is_smart = false
    );

-- Slugs stay unique per-user for private shelves (existing
-- idx_shelves_user_slug); public shelves additionally get a global
-- uniqueness guard so two admins can't both publish "favorites".
CREATE UNIQUE INDEX IF NOT EXISTS idx_shelves_public_slug
    ON shelves(slug) WHERE is_public = true;
