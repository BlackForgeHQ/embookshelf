-- Dev seed data. Loaded via `make seed` (runs inside the postgres container).
--
-- Intentionally minimal: just enough to log in. Libraries and books are
-- meant to be created by the admin through the UI (Settings → Libraries →
-- New library, then scan a folder), so the dev environment mirrors what a
-- real first-time install looks like.

-- Dev admin account (bcrypt hash generated via pgcrypto's crypt()).
-- Credentials: admin@local / changeme
INSERT INTO users (email, password_hash, name, role)
VALUES ('admin@local', crypt('changeme', gen_salt('bf', 10)), 'Admin', 'admin')
ON CONFLICT (email) DO NOTHING;

-- Rename legacy slug from an earlier seed so re-runs on an existing DB
-- pick up the canonical "reading" slug the frontend wires to. Safe no-op
-- on fresh installs.
UPDATE shelves SET slug = 'reading', name = 'Reading Now'
WHERE slug = 'currently-reading';

-- A starter shelf catalog so the admin has somewhere to drop books before
-- building their own. All attached to the admin user; other users start
-- empty and add their own.
INSERT INTO shelves (name, slug, accent, user_id)
SELECT t.name, t.slug, t.accent, u.id
FROM users u, (VALUES
  ('Reading Now', 'reading',   'accent'),
  ('To read',     'to-read',   'teal'),
  ('Favorites',   'favorites', 'rust')
) AS t(name, slug, accent)
WHERE u.email = 'admin@local'
ON CONFLICT (user_id, slug) DO NOTHING;
