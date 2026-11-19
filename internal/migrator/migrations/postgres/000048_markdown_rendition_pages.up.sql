-- The page record of a markdown rendition (ADR-0040): a PDF converts
-- even when some of its pages give no text, and the pages that did not
-- are recorded here instead of being refused or silently dropped.
--
-- page_count is the source document's page count; 0 for a rendition of
-- a format without pages, and for every row converted before this
-- record existed — those rows say nothing about missing pages until the
-- book is converted again (ADR-0040 §5; the bulk run leaves ready rows
-- alone, so that is the per-book request).
--
-- missing_pages names the 1-based pages with no extractable text in the
-- converter's compact form, "1,10-15"; empty when none were missing.
-- Display-ready text rather than an array: it is read whole, by people,
-- and a scanned 900-page book stays one short string.
ALTER TABLE book_markdown_renditions
    ADD COLUMN page_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN missing_pages TEXT NOT NULL DEFAULT '';
