-- Per-user reading progress. Books row stays global; reading state lives here.
CREATE TABLE IF NOT EXISTS user_book_progress (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id      UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    progress     INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    last_read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id)
);

CREATE INDEX IF NOT EXISTS idx_ubp_book ON user_book_progress(book_id);

-- Drop the old per-book progress column; reading state is now per-user.
ALTER TABLE books DROP COLUMN IF EXISTS progress;

-- Move shelves from instance-wide to per-user ownership.
ALTER TABLE shelves
    ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;

-- Best-effort: attach any existing shelves to the first admin. In a fresh
-- install there won't be any rows yet; in a dev install with seeded shelves
-- there won't be an admin yet either (seed runs after migrate), so those rows
-- fall through to the DELETE below and get rebuilt by the updated seed.
UPDATE shelves
   SET user_id = (SELECT id FROM users WHERE role = 'admin' ORDER BY created_at ASC LIMIT 1)
 WHERE user_id IS NULL;
DELETE FROM shelves WHERE user_id IS NULL;

ALTER TABLE shelves ALTER COLUMN user_id SET NOT NULL;

-- Shelf slugs uniqueness moves from global to per-user.
ALTER TABLE shelves DROP CONSTRAINT IF EXISTS shelves_slug_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_shelves_user_slug ON shelves(user_id, slug);
