ALTER TABLE users
    ADD COLUMN oidc_subject TEXT,
    ADD COLUMN oidc_issuer  TEXT;

-- A given (issuer, subject) pair uniquely identifies one user.
CREATE UNIQUE INDEX users_oidc_identity ON users (oidc_issuer, oidc_subject)
    WHERE oidc_subject IS NOT NULL;

-- Allow password_hash to be NULL for OIDC-only users.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
