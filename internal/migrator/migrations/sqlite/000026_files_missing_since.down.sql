DROP INDEX IF EXISTS idx_files_missing_since;
ALTER TABLE files DROP COLUMN missing_since;
