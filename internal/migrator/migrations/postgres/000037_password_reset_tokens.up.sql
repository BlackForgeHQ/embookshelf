-- Single-use password reset tokens. Plaintext is generated as 32 random
-- bytes (crypto/rand) and emailed to the user; only the sha256 digest
-- lives here so a DB read can't replay an outstanding reset. Expiry is
-- 1h enforced server-side; the row stays after consumption with
-- used_at set so the audit trail survives a single sweeper pass. See
-- ADR-0020.
CREATE TABLE password_reset_tokens (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);

CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens(user_id);
CREATE INDEX idx_password_reset_tokens_expires ON password_reset_tokens(expires_at);
