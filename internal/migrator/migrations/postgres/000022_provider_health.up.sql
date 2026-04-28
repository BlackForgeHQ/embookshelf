-- Provider health telemetry. Every Search call writes one of these
-- columns so admins can spot stale tokens / broken scrapers in the
-- Settings panel without having to trigger a test fetch.
--
-- last_success_at timestamps the most recent non-error call; likewise
-- last_error_at + last_error capture the most recent failure. Neither
-- timestamp is on the critical path — writes are non-fatal and happen
-- from the Search goroutines after the result has already been sent
-- to the UI.
ALTER TABLE provider_settings
    ADD COLUMN IF NOT EXISTS last_success_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error      TEXT NOT NULL DEFAULT '';
