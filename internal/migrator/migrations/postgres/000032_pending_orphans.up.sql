-- ADR-0005: S3 edit-time folder rename uses copy + sweeper-deferred
-- delete. pending_orphans queues old keys (and half-rename garbage)
-- for a background sweeper to delete after the grace window.

CREATE TABLE pending_orphans (
    id           BIGSERIAL PRIMARY KEY,
    library_id   UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    eligible_at  TIMESTAMPTZ NOT NULL,
    reason       TEXT NOT NULL,
    book_id      UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (library_id, key)
);

CREATE INDEX pending_orphans_due ON pending_orphans (eligible_at);
