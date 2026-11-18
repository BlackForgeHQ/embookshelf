-- Personal access tokens for the MCP server (ADR-0039). Plaintext is
-- "ebk_" + 32 random bytes (base64url), shown to the user once; only the
-- sha256 digest lives here. prefix is the first 8 characters after
-- "ebk_", kept so a token can be recognised in the UI without being
-- usable. expires_at NULL means the token never expires.
CREATE TABLE api_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   BYTEA NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ
);

CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);
