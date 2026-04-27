-- Per-user annotations: highlights, margin notes, and highlights with
-- commentary attached. One table handles all three variants —
--
--   selected_text != '' && note == ''  → pure highlight
--   selected_text == '' && note != ''  → margin note
--   both populated                     → highlight with commentary
--
-- `locator` is format-specific: an EPUB CFI range for EPUBs, a page:N
-- token for PDFs, or empty for annotations that aren't anchored in the
-- source (e.g. a free-floating reflection the user typed from the book
-- detail page). Mirrors the shared-column pattern we already use for
-- user_book_progress.resume_cfi.
CREATE TABLE IF NOT EXISTS annotations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id       UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    locator       TEXT NOT NULL DEFAULT '',
    selected_text TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    color         TEXT NOT NULL DEFAULT 'accent',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (selected_text <> '' OR note <> '')
);

CREATE INDEX IF NOT EXISTS idx_annotations_user_book
    ON annotations(user_id, book_id, created_at DESC);

-- Notebook view (cross-book list sorted newest-first) leans on this
-- index hot; keep it narrow so it fits whole-table scans cheap.
CREATE INDEX IF NOT EXISTS idx_annotations_user_recent
    ON annotations(user_id, created_at DESC);
