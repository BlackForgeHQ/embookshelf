-- See postgres/000035_shelf_icon.up.sql for the rationale.
ALTER TABLE shelves
    ADD COLUMN icon TEXT NOT NULL DEFAULT 'library';

UPDATE shelves
SET icon = CASE
    WHEN slug = 'reading'  THEN 'book-open'
    WHEN slug = 'finished' THEN 'check-circle-2'
    WHEN slug = 'new'      THEN 'sparkles'
    WHEN slug = 'tofinish' THEN 'flag'
    WHEN slug = 'wishlist' THEN 'bookmark'
    WHEN is_smart = 1      THEN 'sparkles'
    ELSE 'library'
END
WHERE icon = 'library';
