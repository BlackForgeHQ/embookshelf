-- SQLite >= 3.35 supports DROP COLUMN; the targeted modernc.org/sqlite
-- driver is well past that. The columns added in the up-migration are
-- pure additions with no indexes, so dropping them is safe.

ALTER TABLE books DROP COLUMN duration_seconds;
ALTER TABLE books DROP COLUMN narrator;
ALTER TABLE books DROP COLUMN chapters;
