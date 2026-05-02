-- Reverse of 000031_user_identities.up.sql. Best-effort: only the
-- first identity per user is restored to the legacy columns, since
-- the original schema only had room for one. Multi-linked accounts
-- lose data — the down migration is a debugging aid, not a
-- production rollback path.

ALTER TABLE users
    ADD COLUMN oidc_subject TEXT,
    ADD COLUMN oidc_issuer  TEXT;

UPDATE users u
SET oidc_subject = i.subject,
    oidc_issuer  = i.issuer
FROM (
    SELECT DISTINCT ON (user_id) user_id, subject, issuer
    FROM user_identities
    ORDER BY user_id, linked_at ASC
) i
WHERE u.id = i.user_id;

CREATE UNIQUE INDEX users_oidc_identity ON users (oidc_issuer, oidc_subject)
    WHERE oidc_subject IS NOT NULL;

DROP TABLE user_identities;
