-- Reverse of 000031_user_identities.up.sql. Restores only the
-- earliest-linked identity per user; multi-linked accounts lose data
-- (see ADR-0001).

ALTER TABLE users ADD COLUMN oidc_subject TEXT;
ALTER TABLE users ADD COLUMN oidc_issuer  TEXT;

UPDATE users
SET (oidc_subject, oidc_issuer) = (
    SELECT i.subject, i.issuer
    FROM user_identities i
    WHERE i.user_id = users.id
    ORDER BY i.linked_at ASC
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM user_identities i WHERE i.user_id = users.id
);

CREATE UNIQUE INDEX IF NOT EXISTS users_oidc_identity
    ON users (oidc_issuer, oidc_subject)
    WHERE oidc_subject IS NOT NULL;

DROP TABLE IF EXISTS user_identities;
