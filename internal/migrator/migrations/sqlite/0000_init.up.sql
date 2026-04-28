-- Squashed SQLite init: end-state schema equivalent to postgres/000001..000023.
--
-- Type translations applied throughout:
--   UUID PRIMARY KEY DEFAULT gen_random_uuid() → TEXT PRIMARY KEY NOT NULL  (ID supplied by app via db.NewID())
--   UUID (FK)                                  → TEXT
--   BOOLEAN NOT NULL DEFAULT false             → INTEGER NOT NULL DEFAULT 0 CHECK (col IN (0,1))
--   BOOLEAN NOT NULL DEFAULT true              → INTEGER NOT NULL DEFAULT 1 CHECK (col IN (0,1))
--   BOOLEAN (nullable)                         → INTEGER CHECK (col IS NULL OR col IN (0,1))
--   TIMESTAMPTZ NOT NULL DEFAULT now()         → TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
--   TIMESTAMPTZ (nullable)                     → TEXT
--   DATE (nullable)                            → TEXT
--   TEXT[] NOT NULL DEFAULT '{}'               → TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(col))
--   JSONB NOT NULL DEFAULT '{}'                → TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(col))
--   JSONB (nullable)                           → TEXT CHECK (col IS NULL OR json_valid(col))
--   tsvector GENERATED ALWAYS AS (…) STORED   → OMITTED (FTS5 virtual table in Task 6)
--   CREATE EXTENSION pgcrypto                  → OMITTED
--
-- Migration #18 supersedes #9: no library_paths table; libraries has a single
-- path column with a partial unique index.
--
-- Migration #6 supersedes #3 on shelves.slug uniqueness: global slug UNIQUE
-- dropped; replaced with per-user (user_id, slug) unique index.

-- ---------------------------------------------------------------------------
-- libraries
-- Translated from: postgres/000001, 000016, 000018
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS libraries (
    id                  TEXT PRIMARY KEY NOT NULL,
    name                TEXT NOT NULL,
    slug                TEXT NOT NULL,
    -- Added by 000018_library_single_path (replaces library_paths table)
    path                TEXT NOT NULL DEFAULT '',
    last_scanned_at     TEXT,
    file_count          INTEGER NOT NULL DEFAULT 0,
    discovered_count    INTEGER NOT NULL DEFAULT 0,
    -- Added by 000016_library_naming_pattern
    file_naming_pattern TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS libraries_slug_key
    ON libraries (slug);

-- Only enforce path uniqueness when a path has been set (matches PG partial index).
CREATE UNIQUE INDEX IF NOT EXISTS libraries_path_key
    ON libraries (path)
    WHERE path <> '';

-- ---------------------------------------------------------------------------
-- books
-- Translated from: postgres/000001, 000002 (minus tsv), 000007, 000008,
--                  000019, 000020
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS books (
    id              TEXT PRIMARY KEY NOT NULL,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    author          TEXT NOT NULL DEFAULT '',
    format          TEXT NOT NULL DEFAULT 'EPUB',
    year            INTEGER NOT NULL DEFAULT 0,
    -- progress removed by 000006 (moved to user_book_progress)
    rating          INTEGER NOT NULL DEFAULT 0,
    cover_palette   TEXT NOT NULL DEFAULT 'navy',
    -- Added by 000002_book_details
    description     TEXT NOT NULL DEFAULT '',
    isbn            TEXT NOT NULL DEFAULT '',
    publisher       TEXT NOT NULL DEFAULT '',
    series          TEXT NOT NULL DEFAULT '',
    series_index    INTEGER NOT NULL DEFAULT 0,
    tags            TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags)),
    -- Added by 000007_reader
    path            TEXT NOT NULL DEFAULT '',
    -- Added by 000008_covers
    has_cover       INTEGER NOT NULL DEFAULT 0 CHECK (has_cover IN (0,1)),
    cover_mime      TEXT NOT NULL DEFAULT '',
    -- Added by 000019_book_metadata_extended
    subtitle        TEXT NOT NULL DEFAULT '',
    language        TEXT NOT NULL DEFAULT '',
    publish_date    TEXT,
    genres          TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(genres)),
    moods           TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(moods)),
    series_total    INTEGER NOT NULL DEFAULT 0,
    public_reviews  INTEGER CHECK (public_reviews IS NULL OR public_reviews IN (0,1)),
    isbn10          TEXT NOT NULL DEFAULT '',
    age_rating      TEXT NOT NULL DEFAULT '',
    content_rating  TEXT NOT NULL DEFAULT '',
    pages           INTEGER NOT NULL DEFAULT 0,
    -- Added by 000020_book_metadata_locks
    title_locked        INTEGER NOT NULL DEFAULT 0 CHECK (title_locked IN (0,1)),
    subtitle_locked     INTEGER NOT NULL DEFAULT 0 CHECK (subtitle_locked IN (0,1)),
    author_locked       INTEGER NOT NULL DEFAULT 0 CHECK (author_locked IN (0,1)),
    description_locked  INTEGER NOT NULL DEFAULT 0 CHECK (description_locked IN (0,1)),
    publisher_locked    INTEGER NOT NULL DEFAULT 0 CHECK (publisher_locked IN (0,1)),
    series_locked       INTEGER NOT NULL DEFAULT 0 CHECK (series_locked IN (0,1)),
    isbn_locked         INTEGER NOT NULL DEFAULT 0 CHECK (isbn_locked IN (0,1)),
    isbn10_locked       INTEGER NOT NULL DEFAULT 0 CHECK (isbn10_locked IN (0,1)),
    language_locked     INTEGER NOT NULL DEFAULT 0 CHECK (language_locked IN (0,1)),
    publish_date_locked INTEGER NOT NULL DEFAULT 0 CHECK (publish_date_locked IN (0,1)),
    genres_locked       INTEGER NOT NULL DEFAULT 0 CHECK (genres_locked IN (0,1)),
    moods_locked        INTEGER NOT NULL DEFAULT 0 CHECK (moods_locked IN (0,1)),
    tags_locked         INTEGER NOT NULL DEFAULT 0 CHECK (tags_locked IN (0,1)),
    pages_locked        INTEGER NOT NULL DEFAULT 0 CHECK (pages_locked IN (0,1)),
    cover_locked        INTEGER NOT NULL DEFAULT 0 CHECK (cover_locked IN (0,1)),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    deleted_at      TEXT
);

CREATE INDEX IF NOT EXISTS idx_books_library_id ON books(library_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_books_title ON books(title) WHERE deleted_at IS NULL;
-- idx_books_tsv omitted (FTS5 replaces in Task 6)
CREATE INDEX IF NOT EXISTS idx_books_format ON books(format) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_books_path ON books(path) WHERE path <> '';

-- ---------------------------------------------------------------------------
-- users
-- Translated from: postgres/000004, 000014, 000017 (avatar_url), 000023
-- Note: password_hash is nullable to support OIDC-only users (changed in #14).
-- Note: email unique index uses LOWER() for case-insensitive lookup.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id               TEXT PRIMARY KEY NOT NULL,
    email            TEXT NOT NULL,
    password_hash    TEXT,
    name             TEXT NOT NULL DEFAULT '',
    role             TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    -- Added by 000014_oidc
    oidc_subject     TEXT,
    oidc_issuer      TEXT,
    -- Added by 000017_app_settings
    avatar_url       TEXT,
    -- Added by 000023_user_approval_status
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'denied')),
    status_changed_at TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_seen_at     TEXT
);

-- Case-insensitive email uniqueness (matches PG's LOWER(email) unique index).
CREATE UNIQUE INDEX IF NOT EXISTS users_email_key ON users(LOWER(email));

CREATE INDEX IF NOT EXISTS idx_users_email ON users(LOWER(email));

-- OIDC identity uniqueness: (issuer, subject) pair identifies one user.
CREATE UNIQUE INDEX IF NOT EXISTS users_oidc_identity
    ON users (oidc_issuer, oidc_subject)
    WHERE oidc_subject IS NOT NULL;

-- Status index for approval queue (matches PG partial index in #23).
CREATE INDEX IF NOT EXISTS users_status_idx ON users (status) WHERE status <> 'active';

-- ---------------------------------------------------------------------------
-- sessions
-- Translated from: postgres/000004
-- Note: the UUID primary key IS the session token (matches PG schema exactly).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT PRIMARY KEY NOT NULL,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TEXT NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_used_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_sessions_user    ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- ---------------------------------------------------------------------------
-- shelves
-- Translated from: postgres/000003, 000006 (user ownership + slug uniqueness),
--                  000011 (smart shelf rule)
-- Note: global slug UNIQUE dropped in #6; replaced by per-user (user_id, slug).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS shelves (
    id         TEXT PRIMARY KEY NOT NULL,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL,
    accent     TEXT NOT NULL DEFAULT 'brick',
    -- Added by 000006_per_user_data
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Added by 000011_smart_shelves
    is_smart   INTEGER NOT NULL DEFAULT 0 CHECK (is_smart IN (0,1)),
    rule       TEXT CHECK (rule IS NULL OR json_valid(rule)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    -- Smart shelves must have a rule; regular shelves must not.
    CHECK (
        (is_smart = 1 AND rule IS NOT NULL) OR
        (is_smart = 0 AND rule IS NULL)
    )
);

-- Per-user slug uniqueness (replaces global slug UNIQUE from #3).
CREATE UNIQUE INDEX IF NOT EXISTS shelves_user_id_slug_key ON shelves(user_id, slug);

-- ---------------------------------------------------------------------------
-- shelf_books
-- Translated from: postgres/000003
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS shelf_books (
    shelf_id TEXT NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
    book_id  TEXT NOT NULL REFERENCES books(id)   ON DELETE CASCADE,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (shelf_id, book_id)
);

CREATE INDEX IF NOT EXISTS idx_shelf_books_book ON shelf_books(book_id);

-- ---------------------------------------------------------------------------
-- bookdrop_items
-- Translated from: postgres/000005, 000008 (cover_mime)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bookdrop_items (
    id            TEXT PRIMARY KEY NOT NULL,
    path          TEXT NOT NULL,
    file_size     INTEGER NOT NULL DEFAULT 0,
    format        TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'discovered'
                  CHECK (state IN ('discovered','processing','ready','failed','imported','rejected')),
    progress      INTEGER NOT NULL DEFAULT 0,
    error_msg     TEXT NOT NULL DEFAULT '',
    -- Extracted metadata
    title         TEXT NOT NULL DEFAULT '',
    author        TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    language      TEXT NOT NULL DEFAULT '',
    has_cover     INTEGER NOT NULL DEFAULT 0 CHECK (has_cover IN (0,1)),
    -- Added by 000008_covers
    cover_mime    TEXT NOT NULL DEFAULT '',
    -- Set when approved and a book row is created
    book_id       TEXT REFERENCES books(id) ON DELETE SET NULL,
    discovered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS bookdrop_items_path_key ON bookdrop_items(path);
CREATE INDEX IF NOT EXISTS idx_bookdrop_state ON bookdrop_items(state);
CREATE INDEX IF NOT EXISTS idx_bookdrop_discovered_at ON bookdrop_items(discovered_at DESC);

-- ---------------------------------------------------------------------------
-- user_book_progress
-- Translated from: postgres/000006, 000007 (resume_cfi)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_book_progress (
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id      TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    progress     INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    -- Added by 000007_reader
    resume_cfi   TEXT NOT NULL DEFAULT '',
    last_read_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (user_id, book_id)
);

CREATE INDEX IF NOT EXISTS idx_ubp_book ON user_book_progress(book_id);

-- ---------------------------------------------------------------------------
-- annotations
-- Translated from: postgres/000010
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS annotations (
    id            TEXT PRIMARY KEY NOT NULL,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    locator       TEXT NOT NULL DEFAULT '',
    selected_text TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    color         TEXT NOT NULL DEFAULT 'accent',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CHECK (selected_text <> '' OR note <> '')
);

CREATE INDEX IF NOT EXISTS idx_annotations_user_book
    ON annotations(user_id, book_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_annotations_user_recent
    ON annotations(user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- reading_sessions
-- Translated from: postgres/000012
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS reading_sessions (
    id             TEXT PRIMARY KEY NOT NULL,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id        TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    started_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    ended_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    start_progress INTEGER NOT NULL DEFAULT 0 CHECK (start_progress BETWEEN 0 AND 100),
    end_progress   INTEGER NOT NULL DEFAULT 0 CHECK (end_progress BETWEEN 0 AND 100),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_reading_sessions_user_started
    ON reading_sessions(user_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_reading_sessions_user_book_ended
    ON reading_sessions(user_id, book_id, ended_at DESC);

-- ---------------------------------------------------------------------------
-- user_devices
-- Translated from: postgres/000013
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_devices (
    id           TEXT PRIMARY KEY NOT NULL,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    name         TEXT NOT NULL,
    secret       TEXT NOT NULL DEFAULT '',
    config       TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    last_sent_at TEXT,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_user_devices_user
    ON user_devices(user_id, created_at DESC);

-- A user can't add two devices with the same display name (case-insensitive).
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_user_name
    ON user_devices(user_id, LOWER(name));

-- ---------------------------------------------------------------------------
-- provider_settings
-- Translated from: postgres/000015, 000021 (config + priority), 000022 (health)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_settings (
    id              TEXT PRIMARY KEY NOT NULL,
    enabled         INTEGER NOT NULL CHECK (enabled IN (0,1)),
    -- Added by 000021_provider_settings_config
    config          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    priority        INTEGER,
    -- Added by 000022_provider_health
    last_success_at TEXT,
    last_error_at   TEXT,
    last_error      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- ---------------------------------------------------------------------------
-- app_settings
-- Translated from: postgres/000017
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS app_settings (
    name       TEXT PRIMARY KEY NOT NULL,
    value      TEXT NOT NULL CHECK (json_valid(value)),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- ============================================================
-- Full-text search: FTS5 virtual table mirrors title/author/series/description.
--   - content='books' makes it an "external content" FTS table; the
--     virtual table doesn't store its own copy of the text and
--     trigger-driven sync keeps it aligned.
--   - content_rowid='rowid' uses the books table's implicit rowid
--     for cross-references (NOT the books.id TEXT — FTS5 needs an
--     INTEGER content_rowid).
-- The PG side keeps the existing tsvector + GIN index.
-- ============================================================
CREATE VIRTUAL TABLE IF NOT EXISTS books_fts USING fts5(
    title,
    author,
    series,
    description,
    content='books',
    content_rowid='rowid',
    tokenize='unicode61'
);

CREATE TRIGGER IF NOT EXISTS books_fts_after_insert
AFTER INSERT ON books BEGIN
    INSERT INTO books_fts(rowid, title, author, series, description)
    VALUES (new.rowid, new.title, new.author, new.series, new.description);
END;

CREATE TRIGGER IF NOT EXISTS books_fts_after_delete
AFTER DELETE ON books BEGIN
    INSERT INTO books_fts(books_fts, rowid, title, author, series, description)
    VALUES ('delete', old.rowid, old.title, old.author, old.series, old.description);
END;

CREATE TRIGGER IF NOT EXISTS books_fts_after_update
AFTER UPDATE ON books BEGIN
    INSERT INTO books_fts(books_fts, rowid, title, author, series, description)
    VALUES ('delete', old.rowid, old.title, old.author, old.series, old.description);
    INSERT INTO books_fts(rowid, title, author, series, description)
    VALUES (new.rowid, new.title, new.author, new.series, new.description);
END;
