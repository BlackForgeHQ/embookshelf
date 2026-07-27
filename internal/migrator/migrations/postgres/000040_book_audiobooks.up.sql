-- Generated narration of a book, one row per book. See ADR-0025, ADR-0028.
--
-- The audio itself is a normal files row inside the book's own folder —
-- a portable library artifact, not a derived cache — so this table holds
-- only what the files row cannot say: which engine and voice produced it,
-- what state the run is in, and which EPUB it was made from.
CREATE TABLE book_audiobooks (
    book_id     UUID PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    -- pending → running → ready, or failed / canceled. Cancel is a state
    -- the segment workers check before each engine call, because it is
    -- the only stop-loss on a run that may cost $170.
    state       TEXT NOT NULL CHECK (state IN ('pending', 'running', 'ready', 'failed', 'canceled')),
    -- What actually produced it, recorded rather than assumed: the
    -- instance default is editable and per-book overrides are the point
    -- of the generate dialog.
    engine      TEXT NOT NULL,
    voice       TEXT NOT NULL,
    model       TEXT NOT NULL DEFAULT '',
    -- The EPUB this narration was made from. Compared against the book's
    -- current file hash to tell the user the audio predates their newer
    -- copy; never used to invalidate anything automatically, because
    -- throwing away $8 of audio over a re-upload would be worse.
    source_content_hash BYTEA,
    -- The generated files row. This pointer *is* the provenance: every
    -- other files row was ingested. Set at finalize; ON DELETE SET NULL
    -- so removing the audio leaves a legible failed/ready row behind.
    file_id     UUID REFERENCES files(id) ON DELETE SET NULL,
    -- Populated on failure so the UI can say what went wrong without
    -- making the operator read the log.
    error       TEXT NOT NULL DEFAULT '',
    total_chars INTEGER NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The sweeper reaps staging for runs abandoned in a terminal-but-not-done
-- state, and the UI polls for anything still moving.
CREATE INDEX idx_book_audiobooks_state ON book_audiobooks(state, updated_at);

-- One unit of synthesis: one engine call, one River job, one retry.
--
-- A table rather than a JSONB array on the parent, because several
-- chapter workers complete concurrently and read-modify-writing one
-- column loses updates — fixing that with SELECT FOR UPDATE would
-- serialise exactly what the split parallelised (ADR-0028 §5).
--
-- Doubles as the alignment map: (char_start, char_end) against
-- (start_ms, start_ms + duration_ms) is the text-to-audio correspondence
-- that lets reading and listening share one progress value. Persisted
-- from the first version even though the sync itself ships later —
-- regenerating half a gigabyte of audio to recover data we already had
-- would be absurd.
CREATE TABLE book_audiobook_segments (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    book_id       UUID NOT NULL REFERENCES book_audiobooks(book_id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    chapter_index INTEGER NOT NULL,
    chapter_title TEXT NOT NULL DEFAULT '',
    -- Character offsets into the book's extracted text.
    char_start    INTEGER NOT NULL DEFAULT 0,
    char_end      INTEGER NOT NULL DEFAULT 0,
    -- Where this segment's audio sits in the finished file. Written when
    -- the segment completes; start_ms is fixed up at finalize, when every
    -- preceding duration is finally known.
    start_ms      BIGINT NOT NULL DEFAULT 0,
    duration_ms   BIGINT NOT NULL DEFAULT 0,
    -- The staged MP3 under ${DATA_PATH}/audiobooks/{book_id}/.
    staged_path   TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL CHECK (state IN ('pending', 'running', 'done', 'failed')),
    error         TEXT NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Retry re-enqueues by (book, seq); a duplicate would pay twice for
    -- the same audio and corrupt the ordering at finalize.
    UNIQUE (book_id, seq)
);

-- Progress is done-over-total on this index, polled while a run is live.
CREATE INDEX idx_book_audiobook_segments_state ON book_audiobook_segments(book_id, state);
