ALTER TABLE books
    ADD COLUMN IF NOT EXISTS description  TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS isbn         TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS publisher    TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS series       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS series_index INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tags         TEXT[]      NOT NULL DEFAULT '{}';

-- Full-text search over title + author + series + description.
ALTER TABLE books
    ADD COLUMN IF NOT EXISTS tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')),        'A') ||
        setweight(to_tsvector('english', coalesce(author, '')),       'B') ||
        setweight(to_tsvector('english', coalesce(series, '')),       'C') ||
        setweight(to_tsvector('english', coalesce(description, '')),  'D')
    ) STORED;

CREATE INDEX IF NOT EXISTS idx_books_tsv ON books USING GIN(tsv);
CREATE INDEX IF NOT EXISTS idx_books_format ON books(format) WHERE deleted_at IS NULL;
