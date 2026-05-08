-- Per-shelf icon. Stores a lucide-react icon name (kebab-case slug).
-- Server enforces a regex shape only; there is no enumerated allow-list,
-- so this column is presentation data with a UI fallback if the slug
-- doesn't resolve. See ADR-0019.
ALTER TABLE shelves
    ADD COLUMN IF NOT EXISTS icon TEXT NOT NULL DEFAULT 'library';

-- Visual-continuity backfill: map the previously-hardcoded
-- BUILTIN_SHELF_ICONS sidebar entries to their lucide-canonical names so
-- existing rows render the same after upgrade. New rows default to
-- 'library' (regular) — the UI overrides at create-time for smart
-- shelves with 'sparkles'.
UPDATE shelves
SET icon = CASE
    WHEN slug = 'reading'  THEN 'book-open'
    WHEN slug = 'finished' THEN 'check-circle-2'
    WHEN slug = 'new'      THEN 'sparkles'
    WHEN slug = 'tofinish' THEN 'flag'
    WHEN slug = 'wishlist' THEN 'bookmark'
    WHEN is_smart          THEN 'sparkles'
    ELSE 'library'
END
WHERE icon = 'library';
