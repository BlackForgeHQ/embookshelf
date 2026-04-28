-- app_settings is a single key-value store for instance-wide configuration
-- that admins can edit from the UI at runtime. Values are JSONB so each
-- setting can be a scalar, array, or object without schema changes.
CREATE TABLE IF NOT EXISTS app_settings (
    name       text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- avatar_url is set from the OIDC `picture` claim and shown in the sidebar.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS avatar_url text;
