ALTER TABLE provider_settings
    DROP COLUMN IF EXISTS last_success_at,
    DROP COLUMN IF EXISTS last_error_at,
    DROP COLUMN IF EXISTS last_error;
