-- Collapse per-library scan paths down to a single required path stored
-- inline on the libraries row. A library now owns one filesystem root,
-- fixed at creation time, and books are physically placed under it via
-- the file-naming pattern on approval.
ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS path             TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_scanned_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS file_count       INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS discovered_count INTEGER     NOT NULL DEFAULT 0;

-- Back-fill from the oldest library_paths row per library. If a library
-- had multiple paths, the newer ones are dropped — this is a destructive
-- schema change by design (spec: one path per library).
UPDATE libraries l
SET
    path             = COALESCE(lp.path, ''),
    last_scanned_at  = lp.last_scanned_at,
    file_count       = COALESCE(lp.file_count, 0),
    discovered_count = COALESCE(lp.discovered_count, 0)
FROM (
    SELECT DISTINCT ON (library_id)
        library_id,
        path,
        last_scanned_at,
        file_count,
        discovered_count
    FROM library_paths
    ORDER BY library_id, created_at ASC
) lp
WHERE lp.library_id = l.id;

DROP TABLE IF EXISTS library_paths;

-- Two libraries can't share one filesystem root — scanning would race
-- and naming-pattern collisions would overwrite each other. Only apply
-- the UNIQUE constraint to non-empty paths so the '' default (used for
-- freshly-created empty rows, theoretically unreachable after 000018
-- but defensive) doesn't block a second empty library.
CREATE UNIQUE INDEX IF NOT EXISTS libraries_path_key
    ON libraries (path)
    WHERE path <> '';
