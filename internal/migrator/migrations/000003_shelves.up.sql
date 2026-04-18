CREATE TABLE IF NOT EXISTS shelves (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    accent     TEXT NOT NULL DEFAULT 'brick',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS shelf_books (
    shelf_id  UUID NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
    book_id   UUID NOT NULL REFERENCES books(id)   ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (shelf_id, book_id)
);

CREATE INDEX IF NOT EXISTS idx_shelf_books_book ON shelf_books(book_id);
