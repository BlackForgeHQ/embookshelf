ALTER TABLE files ADD COLUMN missing_since TEXT;
CREATE INDEX IF NOT EXISTS idx_files_missing_since ON files(missing_since) WHERE missing_since IS NOT NULL;
