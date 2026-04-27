-- Two deterministic pending users used by admin-approval.spec.ts. The
-- e2e harness loads this via psql before the spec runs.
INSERT INTO users (email, password_hash, name, role, oidc_issuer, oidc_subject, status, status_changed_at)
VALUES
  ('pending-approve@e2e.local', '', 'Pending Approve', 'user', 'https://e2e.local', 'e2e-approve', 'pending', now()),
  ('pending-deny@e2e.local',    '', 'Pending Deny',    'user', 'https://e2e.local', 'e2e-deny',    'pending', now())
ON CONFLICT (email) DO NOTHING;
