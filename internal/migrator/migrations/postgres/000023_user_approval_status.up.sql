ALTER TABLE users
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'pending', 'denied'));

ALTER TABLE users
    ADD COLUMN status_changed_at TIMESTAMPTZ;

CREATE INDEX users_status_idx ON users (status) WHERE status <> 'active';
