-- Drop the dormant libraries.org_mode column (ADR-0003).
-- The column was added in 000025_storage_v2 with a CHECK constraint and
-- DEFAULT 'book_per_folder', but no runtime code ever read or branched
-- on it. ADR-0003 hardcodes book-per-folder semantics; the knob is
-- removed.

ALTER TABLE libraries DROP COLUMN IF EXISTS org_mode;
