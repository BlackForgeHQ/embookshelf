-- Rows have to move before the constraint narrows, or the ADD fails on
-- any run that had a retry in flight. 'failed' is where those segments
-- would have been recorded before this migration, and it is the reading
-- the older code puts on them: settled, and re-enqueued by Retry.
UPDATE book_audiobook_segments SET state = 'failed' WHERE state = 'retrying';

ALTER TABLE book_audiobook_segments
    DROP CONSTRAINT book_audiobook_segments_state_check;

ALTER TABLE book_audiobook_segments
    ADD CONSTRAINT book_audiobook_segments_state_check
    CHECK (state IN ('pending', 'running', 'done', 'failed'));
