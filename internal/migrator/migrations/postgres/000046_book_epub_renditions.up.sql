-- Generated EPUBs (ADR-0034): the tracking row for the EPUB rendered
-- from a PDF book's Markdown rendition by the converter extension.
--
-- The artifact itself IS a files row on the same book (ADR-0025's
-- identity answer reapplied — a user deliverable, not machine feed), so
-- unlike book_markdown_renditions this row carries file_id: provenance
-- is a pointer, not a flag. No is_generated column on files, no scan
-- changes. books.format deliberately stays PDF — nothing recomputes
-- primary format, and in-app reading of the generated EPUB is a
-- deferred rendition-dispatch feature.
--
-- source_content_hash is the PDF's hash, not the markdown's: staleness
-- answers "is this EPUB from the current book" in one comparison, the
-- same question the audiobook row answers.
--
-- error is the loud-failure channel, surfaced verbatim (ADR-0033 §5) —
-- including a failed markdown conversion propagated from the chained
-- stage.
CREATE TABLE book_epub_renditions (
    book_id UUID PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'ready', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    -- The generated files row. SET NULL, not CASCADE: losing the file
    -- (a scan marking it missing and the purge deleting it) leaves a
    -- row that says "was generated, file gone", which the UI can offer
    -- to regenerate.
    file_id UUID REFERENCES files(id) ON DELETE SET NULL,
    source_content_hash BYTEA,
    converter_version TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
