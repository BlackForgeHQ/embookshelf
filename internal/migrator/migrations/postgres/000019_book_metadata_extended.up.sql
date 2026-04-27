-- Extend the books table with the broader metadata surface the edit page
-- exposes (subtitle, publish date, genres, moods, series total, public
-- review toggle, ISBN-10, age/content ratings, page count, language).
--
-- Keep the legacy `isbn` column as-is; the edit UI treats it as ISBN-13
-- and writes ISBN-10 to the new column. `year` is preserved — it's a
-- cheap sort key — and `publish_date` is the richer nullable DATE.
-- public_reviews is nullable so the admin can leave it explicitly unset
-- (rendered as "No Value" in the form).

ALTER TABLE books
    ADD COLUMN IF NOT EXISTS subtitle       TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS language       TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS publish_date   DATE,
    ADD COLUMN IF NOT EXISTS genres         TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS moods          TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS series_total   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS public_reviews BOOLEAN,
    ADD COLUMN IF NOT EXISTS isbn10         TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS age_rating     TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS content_rating TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS pages          INTEGER NOT NULL DEFAULT 0;
