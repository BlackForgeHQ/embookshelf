-- See postgres/000037_password_reset_tokens.up.sql for the rationale.
CREATE TABLE password_reset_tokens (
    token_hash BLOB PRIMARY KEY NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens(user_id);
CREATE INDEX idx_password_reset_tokens_expires ON password_reset_tokens(expires_at);
