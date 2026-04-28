ALTER TABLE books
    DROP COLUMN IF EXISTS subtitle,
    DROP COLUMN IF EXISTS language,
    DROP COLUMN IF EXISTS publish_date,
    DROP COLUMN IF EXISTS genres,
    DROP COLUMN IF EXISTS moods,
    DROP COLUMN IF EXISTS series_total,
    DROP COLUMN IF EXISTS public_reviews,
    DROP COLUMN IF EXISTS isbn10,
    DROP COLUMN IF EXISTS age_rating,
    DROP COLUMN IF EXISTS content_rating,
    DROP COLUMN IF EXISTS pages;
