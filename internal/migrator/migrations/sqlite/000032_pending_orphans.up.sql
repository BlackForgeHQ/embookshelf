-- ADR-0005: pending_orphans queues old keys for the S3 rename
-- sweeper. SQLite mirror of postgres/000032.
--
-- Type translations:
--   BIGSERIAL                   → INTEGER PRIMARY KEY AUTOINCREMENT
--   UUID                        → TEXT
--   TIMESTAMPTZ                 → TEXT, ISO8601 via strftime
--   NOW()                       → strftime('%Y-%m-%dT%H:%M:%fZ','now')

CREATE TABLE IF NOT EXISTS pending_orphans (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    eligible_at  TEXT NOT NULL,
    reason       TEXT NOT NULL,
    book_id      TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (library_id, key)
);

CREATE INDEX IF NOT EXISTS pending_orphans_due ON pending_orphans (eligible_at);
