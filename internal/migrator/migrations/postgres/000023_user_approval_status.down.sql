DROP INDEX IF EXISTS users_status_idx;
ALTER TABLE users DROP COLUMN IF EXISTS status_changed_at;
ALTER TABLE users DROP COLUMN IF EXISTS status;
