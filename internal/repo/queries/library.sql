-- name: ListLibraries :many
SELECT id, name, slug, created_at
FROM libraries
ORDER BY created_at ASC;

-- name: GetLibraryBySlug :one
SELECT id, name, slug, created_at
FROM libraries
WHERE slug = $1;

-- name: ListBooksByLibrarySlug :many
SELECT b.id, b.library_id, b.title, b.author, b.format, b.year,
       b.progress, b.rating, b.cover_palette, b.created_at
FROM books b
JOIN libraries l ON l.id = b.library_id
WHERE l.slug = $1 AND b.deleted_at IS NULL
ORDER BY b.title ASC;
