-- ADR-0001: replace users.oidc_subject/oidc_issuer with a dedicated
-- user_identities table. SQLite mirror of postgres/000031.
--
-- Type translations:
--   UUID                        → TEXT (ID supplied by app via db.NewID())
--   TIMESTAMPTZ                 → TEXT, ISO8601 via strftime
--   gen_random_uuid()           → no default; app supplies id

CREATE TABLE IF NOT EXISTS user_identities (
    id            TEXT PRIMARY KEY NOT NULL,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email         TEXT,
    linked_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_login_at TEXT,
    UNIQUE (issuer, subject),
    UNIQUE (user_id, provider)
);

CREATE INDEX IF NOT EXISTS user_identities_user_id ON user_identities (user_id);

-- Backfill from the legacy columns. SQLite has no gen_random_uuid;
-- use lower(hex(randomblob(16))) to mint a 32-char hex id, which
-- matches db.NewID()'s output shape closely enough for backfill.
INSERT INTO user_identities (id, user_id, provider, issuer, subject, linked_at)
SELECT
    lower(hex(randomblob(16))),
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

ALTER TABLE users DROP COLUMN oidc_subject;
ALTER TABLE users DROP COLUMN oidc_issuer;
