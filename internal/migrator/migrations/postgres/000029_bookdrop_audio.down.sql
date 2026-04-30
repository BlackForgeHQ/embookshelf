ALTER TABLE bookdrop_items
    DROP COLUMN IF EXISTS chapters,
    DROP COLUMN IF EXISTS narrator,
    DROP COLUMN IF EXISTS duration_seconds;
