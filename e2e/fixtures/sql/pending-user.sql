-- Two deterministic pending users used by admin-approval.spec.ts. The
-- e2e harness loads this via psql before the spec runs.
--
-- The OIDC identity lives in user_identities, not on the users row. The
-- columns this fixture used to write — users.oidc_issuer, oidc_subject —
-- went away when an account gained the ability to hold more than one
-- identity, and the fixture kept writing them: the INSERT errored, the
-- psql call exited non-zero, and both specs in the file died in
-- beforeEach. Invisible, because the suite could not run (#216).
INSERT INTO users (email, password_hash, name, role, status, status_changed_at)
VALUES
  ('pending-approve@e2e.local', '', 'Pending Approve', 'user', 'pending', now()),
  ('pending-deny@e2e.local',    '', 'Pending Deny',    'user', 'pending', now())
ON CONFLICT (email) DO NOTHING;

-- What makes them OIDC users rather than local ones: an identity row
-- naming the issuer they came from. Selected off the users table by
-- email so a re-run that kept the users adds no second identity.
INSERT INTO user_identities (user_id, provider, issuer, subject, email)
SELECT u.id, 'oidc', 'https://e2e.local', t.subject, u.email
FROM users u
JOIN (VALUES
  ('pending-approve@e2e.local', 'e2e-approve'),
  ('pending-deny@e2e.local',    'e2e-deny')
) AS t(email, subject) ON t.email = u.email
ON CONFLICT (issuer, subject) DO NOTHING;
