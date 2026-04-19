DROP INDEX IF EXISTS users_oidc_identity;
ALTER TABLE users
    DROP COLUMN IF EXISTS oidc_subject,
    DROP COLUMN IF EXISTS oidc_issuer;
-- NOTE: password_hash NOT NULL constraint is not restored because existing
-- OIDC-only rows would violate it.
