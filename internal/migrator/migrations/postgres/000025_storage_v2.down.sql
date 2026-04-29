-- 000025_storage_v2.down.sql (PG)
-- Reverses the additive changes from 000025_storage_v2.up.sql.
-- Legacy books.path / libraries.path were never touched, so no restore needed.

ALTER TABLE bookdrop_items DROP COLUMN IF EXISTS content_hash;

ALTER TABLE books
    DROP COLUMN IF EXISTS folder_path,
    DROP COLUMN IF EXISTS uuid;

DROP INDEX IF EXISTS idx_files_library;
DROP INDEX IF EXISTS idx_files_book;
DROP INDEX IF EXISTS idx_files_hash;
DROP TABLE IF EXISTS files;

ALTER TABLE libraries
    DROP COLUMN IF EXISTS org_mode,
    DROP COLUMN IF EXISTS root,
    DROP COLUMN IF EXISTS backend_id;

DROP TABLE IF EXISTS storage_backends;
