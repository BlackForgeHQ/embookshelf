-- 000025_storage_v2.down.sql (SQLite)
-- Reverses the additive changes from 000025_storage_v2.up.sql.
-- Legacy books.path / libraries.path were never touched, so no restore needed.
--
-- Note: SQLite does not support DROP COLUMN on tables with indexes or
-- constraints referencing the column in older versions, but SQLite 3.35+
-- (used by this project) supports ALTER TABLE ... DROP COLUMN.
-- The IF NOT EXISTS guard on the up migration means these may be no-ops
-- on a partially-applied migration — that is safe.

ALTER TABLE bookdrop_items DROP COLUMN IF EXISTS content_hash;

ALTER TABLE books DROP COLUMN IF EXISTS folder_path;
ALTER TABLE books DROP COLUMN IF EXISTS uuid;

DROP INDEX IF EXISTS idx_files_library;
DROP INDEX IF EXISTS idx_files_book;
DROP INDEX IF EXISTS idx_files_hash;
DROP TABLE IF EXISTS files;

ALTER TABLE libraries DROP COLUMN IF EXISTS org_mode;
ALTER TABLE libraries DROP COLUMN IF EXISTS root;
ALTER TABLE libraries DROP COLUMN IF EXISTS backend_id;

DROP TABLE IF EXISTS storage_backends;
