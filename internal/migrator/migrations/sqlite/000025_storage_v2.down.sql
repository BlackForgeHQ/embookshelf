-- 000025_storage_v2.down.sql (SQLite)
-- Reverses the additive changes from 000025_storage_v2.up.sql.
-- Legacy books.path / libraries.path were never touched, so no restore needed.
--
-- SQLite 3.35+ supports ALTER TABLE ... DROP COLUMN, but it does NOT
-- accept an IF EXISTS clause on it — that is a Postgres extension, and
-- writing it here made every statement below a syntax error, so this
-- file could not run at all (#275). DROP INDEX and DROP TABLE do take
-- IF EXISTS, which is why only the column drops changed.
--
-- The up migration's IF NOT EXISTS guards therefore have no counterpart
-- here: reverting a partially-applied 000025 will fail on the first
-- column that was never added. Reverting a fully-applied one, which is
-- what a down chain does, works.

ALTER TABLE bookdrop_items DROP COLUMN content_hash;

ALTER TABLE books DROP COLUMN folder_path;

-- Before the column it indexes: SQLite refuses to drop a column an index
-- still references, whatever the version. The up migration creates this
-- one because SQLite will not take ADD COLUMN ... UNIQUE.
DROP INDEX IF EXISTS books_uuid_key;
ALTER TABLE books DROP COLUMN uuid;

DROP INDEX IF EXISTS idx_files_library;
DROP INDEX IF EXISTS idx_files_book;
DROP INDEX IF EXISTS idx_files_hash;
DROP TABLE IF EXISTS files;

ALTER TABLE libraries DROP COLUMN org_mode;
ALTER TABLE libraries DROP COLUMN root;
ALTER TABLE libraries DROP COLUMN backend_id;

DROP TABLE IF EXISTS storage_backends;
