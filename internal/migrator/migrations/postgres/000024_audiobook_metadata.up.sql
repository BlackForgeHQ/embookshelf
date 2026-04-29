-- Audiobook metadata: duration, narrator, and chapter list.
--
-- Used by Phase 2 of the multi-format reader work (MP3 / M4B). Other
-- formats leave these NULL — the reader only consults them when
-- format = 'MP3'.
--
-- chapters is JSONB so the column can hold the canonical chapter shape
-- ([{title, start_s, end_s}, ...]) without a join table; reader queries
-- pull the whole document at once and the audiobook UI is the only
-- consumer today.

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS duration_seconds INTEGER,
    ADD COLUMN IF NOT EXISTS narrator         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS chapters         JSONB;
