-- Audiobook metadata on bookdrop_items: duration, narrator, chapters.
--
-- Pre-existing flow re-ran AudioProcessor at approval to fetch these
-- fields off the placed file (the bookdrop schema didn't carry them).
-- Folding them into the bookdrop row collapses the parallel pipeline:
-- audio extraction now happens once at ingest like every other format,
-- and Approve becomes a pure DB transition.
--
-- chapters mirrors books.chapters (JSONB) so the same scan logic
-- works at both layers.

ALTER TABLE bookdrop_items
    ADD COLUMN IF NOT EXISTS duration_seconds INTEGER,
    ADD COLUMN IF NOT EXISTS narrator         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS chapters         JSONB;
