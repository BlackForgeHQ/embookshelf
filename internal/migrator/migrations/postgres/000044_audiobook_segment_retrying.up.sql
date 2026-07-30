-- A segment River is going to try again is not a failed segment.
-- See ADR-0032.
--
-- The worker recorded 'failed' for both kinds of failure and then either
-- returned nil (permanent, so River stops) or returned the error
-- (transient, so River retries). The row could not tell the two apart,
-- and Coverage counts 'failed' as settled — so a sibling segment landing
-- while a retry was still outstanding read one settled failure, concluded
-- the run failed, and the retry that then succeeded was a no-op, because
-- the disposition refuses to act on a failed run. A purely transient
-- upstream hiccup permanently failed a run whose segments all eventually
-- succeeded, and which of the two interleavings happened was a timing
-- coincidence (#263).
--
-- 'retrying' is therefore outstanding, not settled: it is counted in
-- neither the done nor the failed column, so the run stays running until
-- the retry lands or River's attempts run out — at which point the worker
-- writes 'failed' and the run concludes as it always did.
ALTER TABLE book_audiobook_segments
    DROP CONSTRAINT book_audiobook_segments_state_check;

ALTER TABLE book_audiobook_segments
    ADD CONSTRAINT book_audiobook_segments_state_check
    CHECK (state IN ('pending', 'running', 'retrying', 'done', 'failed'));
