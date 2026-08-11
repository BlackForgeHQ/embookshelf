-- 55 downloadable books in a dedicated library: enough to cross the
-- OPDS page boundary at 50/page. Titles are prefixed so the spec's
-- cleanup can delete them without touching anything else.
INSERT INTO libraries (name, slug, path)
VALUES ('E2E OPDS Paging', 'e2e-opds-paging', '/tmp/e2e-opds-paging')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO books (library_id, title, author, format, path)
SELECT
  l.id,
  format('e2e-opds-page-%s', lpad(i::text, 3, '0')),
  'e2e fixture',
  'EPUB',
  format('/tmp/e2e-opds-paging/%s.epub', lpad(i::text, 3, '0'))
FROM libraries l, generate_series(0, 54) AS i
WHERE l.slug = 'e2e-opds-paging';
