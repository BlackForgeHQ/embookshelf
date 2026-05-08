-- Admin-issued invitations. Plaintext token (32 random bytes,
-- base64url) is emailed; only sha256 lives here. accepted_at + user_id
-- set together when the invitee chooses a password and a users row is
-- materialised. Default expiry 7 days enforced at write time. See
-- ADR-0020.
CREATE TABLE user_invites (
    token_hash  BYTEA PRIMARY KEY,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    invited_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_user_invites_email ON user_invites(lower(email));
CREATE INDEX idx_user_invites_expires ON user_invites(expires_at);
CREATE INDEX idx_user_invites_invited_by ON user_invites(invited_by);
