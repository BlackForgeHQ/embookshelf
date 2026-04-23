ALTER TABLE provider_settings
    DROP COLUMN IF EXISTS config,
    DROP COLUMN IF EXISTS priority;
