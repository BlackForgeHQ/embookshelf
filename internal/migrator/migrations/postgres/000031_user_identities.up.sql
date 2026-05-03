-- ADR-0007: replace users.oidc_subject/oidc_issuer with a dedicated
-- user_identities table so a user can link multiple providers
-- (Google + GitHub + generic). Forward-only migration: existing rows
-- are copied across in the same statement and the old columns are
-- dropped at the end.

CREATE TABLE user_identities (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email         TEXT,
    linked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ,
    UNIQUE (issuer, subject),
    UNIQUE (user_id, provider)
);

CREATE INDEX user_identities_user_id ON user_identities (user_id);

-- Backfill from the legacy columns. Provider slug is derived from the
-- issuer URL for the two well-known IdPs; everything else is treated
-- as the generic OIDC slot.
INSERT INTO user_identities (user_id, provider, issuer, subject, linked_at)
SELECT
    id,
    CASE
        WHEN oidc_issuer LIKE '%accounts.google.com%' THEN 'google'
        WHEN oidc_issuer LIKE '%github%'              THEN 'github'
        ELSE 'generic'
    END,
    oidc_issuer,
    oidc_subject,
    created_at
FROM users
WHERE oidc_subject IS NOT NULL AND oidc_issuer IS NOT NULL;

DROP INDEX IF EXISTS users_oidc_identity;

ALTER TABLE users
    DROP COLUMN oidc_subject,
    DROP COLUMN oidc_issuer;
