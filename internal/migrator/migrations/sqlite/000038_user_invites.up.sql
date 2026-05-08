-- See postgres/000038_user_invites.up.sql for the rationale.
CREATE TABLE user_invites (
    token_hash  BLOB PRIMARY KEY NOT NULL,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    invited_by  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at  TEXT NOT NULL,
    accepted_at TEXT,
    user_id     TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_user_invites_email ON user_invites(lower(email));
CREATE INDEX idx_user_invites_expires ON user_invites(expires_at);
CREATE INDEX idx_user_invites_invited_by ON user_invites(invited_by);
