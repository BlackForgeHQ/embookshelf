-- Per-user destination address for the Send-to-Kindle action. Empty
-- (NULL or '') means the user has not configured Send-to-Kindle and
-- the UI surfaces a "Set Kindle email" link instead of the send
-- button. Validated server-side as `^[a-z0-9._-]+@kindle\.com$`. See
-- ADR-0021.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS kindle_email TEXT NOT NULL DEFAULT '';
