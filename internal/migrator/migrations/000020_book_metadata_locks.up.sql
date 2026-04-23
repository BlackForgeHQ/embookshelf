-- Per-field lock flags. When a field is locked, the apply-metadata flow
-- (PUT /books/:id/metadata) skips writing to it even if a candidate has a
-- non-empty value. This protects hand-curated records from being clobbered
-- by a subsequent provider pull.
--
-- We cover only the fields the edit UI and provider matches populate — no
-- lock for on-disk path, progress, resume CFI, or timestamps, which aren't
-- part of a metadata refresh.

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS title_locked          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS subtitle_locked       BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS author_locked         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS description_locked    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS publisher_locked      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS series_locked         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS isbn_locked           BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS isbn10_locked         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS language_locked       BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS publish_date_locked   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS genres_locked         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS moods_locked          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS tags_locked           BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS pages_locked          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS cover_locked          BOOLEAN NOT NULL DEFAULT FALSE;
