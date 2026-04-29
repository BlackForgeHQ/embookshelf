ALTER TABLE books
    DROP COLUMN IF EXISTS duration_seconds,
    DROP COLUMN IF EXISTS narrator,
    DROP COLUMN IF EXISTS chapters;
