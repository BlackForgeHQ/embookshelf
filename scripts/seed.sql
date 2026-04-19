-- Dev seed data. Loaded via `make seed` (runs inside the postgres container).

-- Dev admin account (bcrypt hash generated via pgcrypto's crypt()).
-- Credentials: admin@local / changeme
INSERT INTO users (email, password_hash, name, role)
VALUES ('admin@local', crypt('changeme', gen_salt('bf', 10)), 'Admin', 'admin')
ON CONFLICT (email) DO NOTHING;

INSERT INTO libraries (name, slug) VALUES
  ('Main',       'main'),
  ('Comics',     'comics'),
  ('Audiobooks', 'audiobooks')
ON CONFLICT (slug) DO NOTHING;

WITH main AS (SELECT id FROM libraries WHERE slug = 'main')
INSERT INTO books (library_id, title, author, format, year, rating, cover_palette, description, isbn, publisher, series, series_index, tags)
SELECT main.id, t.title, t.author, t.format, t.year, t.rating, t.palette, t.descr, t.isbn, t.publisher, t.series, t.series_index, t.tags
FROM main, (VALUES
  ('The Name of the Rose',     'Umberto Eco',         'EPUB', 1980, 5, 'brick',
   'A Franciscan friar and his novice investigate a string of suspicious deaths in a 14th-century abbey.', '978-0544176560', 'Harcourt', '', 0, ARRAY['mystery','historical']),
  ('Piranesi',                  'Susanna Clarke',      'EPUB', 2020, 5, 'teal',
   'A man lives alone in a vast, labyrinthine house whose halls are filled with statues and tides. He has never questioned his circumstances — until now.', '978-1635575637', 'Bloomsbury', '', 0, ARRAY['fantasy','literary']),
  ('Gödel, Escher, Bach',       'Douglas Hofstadter',  'PDF',  1979, 4, 'ochre',
   'An Eternal Golden Braid — a meditation on self-reference, consciousness, and the common threads between mathematics, art, and music.', '978-0465026562', 'Basic Books', '', 0, ARRAY['philosophy','science']),
  ('A Wizard of Earthsea',      'Ursula K. Le Guin',   'EPUB', 1968, 5, 'forest',
   'A young sorcerer on the island of Gont learns that the price of power is learning one’s own true name.', '978-0547773742', 'Houghton Mifflin', 'Earthsea', 1, ARRAY['fantasy','young adult']),
  ('The Tombs of Atuan',        'Ursula K. Le Guin',   'EPUB', 1971, 5, 'plum',
   'Tenar, high priestess of the Nameless Ones, discovers a thief in the labyrinth beneath her temple.', '978-0689845369', 'Atheneum', 'Earthsea', 2, ARRAY['fantasy','young adult']),
  ('Blindsight',                'Peter Watts',         'EPUB', 2006, 4, 'navy',
   'First contact in the outer solar system goes unexpectedly — and the crew sent to investigate may not themselves be quite what we think of as human.', '978-0765319647', 'Tor', 'Firefall', 1, ARRAY['sci-fi','hard sf']),
  ('The City & The City',       'China Miéville',      'EPUB', 2009, 4, 'plum',
   'Two cities occupy the same geographical space; their citizens must studiously "unsee" each other. A murder drags Inspector Borlú across the divide.', '978-0345497529', 'Del Rey', '', 0, ARRAY['mystery','weird fiction']),
  ('House of Leaves',           'Mark Z. Danielewski', 'PDF',  2000, 5, 'ink',
   'A family moves into a house that is slightly larger on the inside than the outside. The documentary they make about it becomes something else entirely.', '978-0375703768', 'Pantheon', '', 0, ARRAY['horror','experimental']),
  ('The Left Hand of Darkness', 'Ursula K. Le Guin',   'EPUB', 1969, 5, 'olive',
   'An envoy to the planet Gethen grapples with a society whose inhabitants have no fixed gender.', '978-0441478125', 'Ace', 'Hainish Cycle', 4, ARRAY['sci-fi','literary']),
  ('Annihilation',              'Jeff VanderMeer',     'EPUB', 2014, 4, 'forest',
   'The twelfth expedition into Area X — a mysterious zone cut off from the rest of the continent — may be the last.', '978-0374104092', 'FSG', 'Southern Reach', 1, ARRAY['sci-fi','horror']),
  ('Saga, Volume 1',            'Brian K. Vaughan',    'CBZ',  2012, 5, 'rust',
   'Two soldiers from opposite sides of an intergalactic war fall in love and flee with their newborn daughter.', '978-1607066019', 'Image Comics', 'Saga', 1, ARRAY['comics','space opera']),
  ('The Three-Body Problem',    'Cixin Liu',           'MP3',  2008, 4, 'brick',
   'Cultural-revolution-era radio astronomy, a very strange videogame, and first contact with a civilization in crisis.', '978-0765382030', 'Tor', 'Remembrance of Earth''s Past', 1, ARRAY['sci-fi','audiobook'])
) AS t(title, author, format, year, rating, palette, descr, isbn, publisher, series, series_index, tags)
ON CONFLICT DO NOTHING;

-- Rename legacy slug from an earlier seed so re-runs on an existing DB
-- pick up the canonical "reading" slug the frontend wires to.
UPDATE shelves SET slug = 'reading', name = 'Reading Now'
WHERE slug = 'currently-reading';

-- Shelves (now per-user, attached to the admin).
INSERT INTO shelves (name, slug, accent, user_id)
SELECT t.name, t.slug, t.accent, u.id
FROM users u, (VALUES
  ('Reading Now', 'reading', 'accent'),
  ('To read',           'to-read',           'teal'),
  ('Favorites',         'favorites',         'rust')
) AS t(name, slug, accent)
WHERE u.email = 'admin@local'
ON CONFLICT (user_id, slug) DO NOTHING;

-- Shelf memberships — key off the admin's shelves and book titles (seeded IDs are random).
INSERT INTO shelf_books (shelf_id, book_id)
SELECT s.id, b.id
FROM shelves s
JOIN users u ON u.id = s.user_id AND u.email = 'admin@local'
JOIN books  b ON
  (s.slug = 'reading' AND b.title IN ('The Name of the Rose','Gödel, Escher, Bach','The Three-Body Problem')) OR
  (s.slug = 'favorites'         AND b.title IN ('Piranesi','A Wizard of Earthsea','The Left Hand of Darkness','House of Leaves')) OR
  (s.slug = 'to-read'           AND b.title IN ('Blindsight','The Tombs of Atuan','Annihilation'))
ON CONFLICT DO NOTHING;

-- Seed a little progress so the UI has something to render.
INSERT INTO user_book_progress (user_id, book_id, progress)
SELECT u.id, b.id, t.pct
FROM users u, books b, (VALUES
  ('The Name of the Rose',    42),
  ('Piranesi',                100),
  ('Gödel, Escher, Bach',      17),
  ('A Wizard of Earthsea',     88),
  ('The City & The City',      64),
  ('House of Leaves',          12),
  ('The Left Hand of Darkness',100),
  ('Annihilation',             33),
  ('Saga, Volume 1',           100),
  ('The Three-Body Problem',   58)
) AS t(title, pct)
WHERE u.email = 'admin@local' AND b.title = t.title
ON CONFLICT (user_id, book_id) DO UPDATE SET progress = EXCLUDED.progress;
