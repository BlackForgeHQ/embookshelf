-- Markdown renditions (ADR-0033 §4-5): the tracking row for a book's
-- machine-readable text, produced from a Convertible-format file (PDF
-- today — formats embookshelf cannot read natively) by the converter
-- extension and written through storage.Storage into the book's own
-- folder.
--
-- One row per book, like book_reading_guides and book_audiobooks: the
-- rendition is a property of the Book, and regenerating overwrites.
--
-- Deliberately NOT a files row. The audiobook's MP3 is the thing the
-- user wanted — downloadable, scan-tracked, delete-tracked (ADR-0025).
-- The markdown is machine feed, like a guide in kind even though it
-- lives beside the book as a file: scan's relocate pass hashes it, finds
-- no files row, and explicitly no-ops (ADR-0018), so the catalog never
-- sees it. This table owns the artifact's whole lifecycle; `location`
-- is the storage key the bytes live at.
--
-- source_content_hash is provenance, not a trigger: when it no longer
-- matches the book's current primary file, the rendition is *stale* —
-- labelled, surfaced, never auto-invalidated (same rule as
-- book_audiobooks.source_content_hash).
--
-- error is the loud-failure channel: "converter extension is not
-- configured" and "failed: <reason>" are different answers and both are
-- surfaced verbatim (ADR-0033 §5). Empty while nothing is wrong.
CREATE TABLE book_markdown_renditions (
    book_id UUID PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    -- pending: enqueued, no worker has picked it up.
    -- running: a worker is converting.
    -- ready: location holds current bytes for source_content_hash.
    -- failed: error says why, verbatim.
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'ready', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    -- Storage key of the markdown file inside the book's library,
    -- e.g. "Author/Title/Title.md". Empty until first ready.
    location TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    -- Hash of the primary file the markdown was converted from.
    source_content_hash BYTEA,
    -- X-Converter-Version of the sidecar that produced the bytes.
    converter_version TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
