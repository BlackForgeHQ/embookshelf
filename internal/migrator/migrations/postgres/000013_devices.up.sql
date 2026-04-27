-- Per-user registered destinations for "send to device" (reMarkable Paper
-- Pro today; Kindle, Kobo, generic OPDS-push later). Each row is a paired
-- device: the pairing secret (e.g. reMarkable device token) lives in
-- `secret`; operational-level config (display label, cloud endpoint
-- overrides, folder target) lives in `config` as JSONB.
CREATE TABLE IF NOT EXISTS user_devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    -- Long-lived pairing/auth material (reMarkable device token, Kindle
    -- email, SSH key fingerprint, ...). Treat as secret — never expose
    -- over the API.
    secret        TEXT NOT NULL DEFAULT '',
    config        JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Last successful push timestamp + error line for surface in the UI.
    last_sent_at  TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_devices_user
    ON user_devices(user_id, created_at DESC);

-- A user can't add two devices with the same display name — avoids
-- confusion in dropdowns. The constraint still lets two different users
-- both register devices called "reMarkable".
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_user_name
    ON user_devices(user_id, lower(name));
