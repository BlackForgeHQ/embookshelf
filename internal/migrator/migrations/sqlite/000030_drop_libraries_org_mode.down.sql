-- Restore libraries.org_mode for downgrades back to 000029.
-- Mirrors the column shape from 000025_storage_v2.up.sql (SQLite).

ALTER TABLE libraries ADD COLUMN org_mode TEXT NOT NULL DEFAULT 'book_per_folder'
    CHECK (org_mode IN ('book_per_file', 'book_per_folder'));
