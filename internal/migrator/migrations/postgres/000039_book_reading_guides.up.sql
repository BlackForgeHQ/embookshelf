-- LLM-written orientation for one book: what it is about, who it suits,
-- who it does not, which reader problems it addresses. See ADR-0024.
--
-- Deliberately its own table rather than columns on books: a column would
-- be carried by UpdateMetadata into the JSON sidecar and the file's own
-- embedded metadata (ADR-0001), shipping machine-written commentary
-- inside the reader's EPUB as if it were publisher metadata.
--
-- One row per book. Regeneration is an UPSERT on the primary key.
CREATE TABLE book_reading_guides (
    book_id      UUID PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    about        TEXT NOT NULL DEFAULT '',
    audience     TEXT NOT NULL DEFAULT '',
    not_for      TEXT NOT NULL DEFAULT '',
    problems     TEXT NOT NULL DEFAULT '',
    -- What the guide was built from. 'full_text' only where the format
    -- can yield it (EPUB today); everything else gets 'metadata', which
    -- the UI surfaces because such a guide leans on the model's prior
    -- knowledge of the title rather than on the book itself.
    source_kind  TEXT NOT NULL CHECK (source_kind IN ('full_text', 'metadata')),
    -- Provenance, so a guide written by a weaker model can be found and
    -- regenerated later.
    model        TEXT NOT NULL DEFAULT '',
    language     TEXT NOT NULL DEFAULT '',
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set when a human edits the text. A bulk Guide run skips these so it
    -- cannot erase hand-written work; the per-book button still
    -- overwrites, because there the user is looking at what they replace.
    edited_by_user BOOLEAN NOT NULL DEFAULT false
);

-- Bulk runs select the books that still need a guide, and skip the
-- hand-edited ones.
CREATE INDEX idx_book_reading_guides_edited ON book_reading_guides(edited_by_user);
