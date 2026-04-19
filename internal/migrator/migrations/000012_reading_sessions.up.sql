-- Reading sessions. Every call to ProgressService.Set either extends the
-- user's most recent session for that book (when the gap since the last
-- tick is under the merge window) or starts a new one. Duration is
-- derived at query time from ended_at - started_at; no cron needed.
--
-- Both timestamps are kept stale-forwarding: started_at is the tick
-- that opened the session, ended_at is the most recent tick within the
-- window. end_progress is the latest percent we saw, start_progress is
-- the percent at session open — (end_progress - start_progress) is the
-- per-session delta.
CREATE TABLE IF NOT EXISTS reading_sessions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id        UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    start_progress INTEGER NOT NULL DEFAULT 0 CHECK (start_progress BETWEEN 0 AND 100),
    end_progress   INTEGER NOT NULL DEFAULT 0 CHECK (end_progress BETWEEN 0 AND 100),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Heatmap + streak queries walk sessions by (user_id, started_at DESC).
CREATE INDEX IF NOT EXISTS idx_reading_sessions_user_started
    ON reading_sessions(user_id, started_at DESC);

-- RecordTick needs to find the most recent session for (user, book) fast.
CREATE INDEX IF NOT EXISTS idx_reading_sessions_user_book_ended
    ON reading_sessions(user_id, book_id, ended_at DESC);
