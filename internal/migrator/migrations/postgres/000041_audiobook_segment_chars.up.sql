-- The segmentation cap a narration run was planned with. See #189.
--
-- Engine, voice and model were already pinned on the run so an admin
-- editing the settings mid-run could not produce a book narrated half in
-- one voice. The cap that decided where the segments *begin and end* was
-- not: every segment job re-extracts the EPUB — deliberately, so the file
-- stays the source of truth for what a character range contains — and it
-- read the cap from the live settings row again each time. Editing the
-- setting while a run was in flight failed every remaining segment, after
-- the money for the earlier ones was already spent.
--
-- The default backfills existing rows with the value they were in fact
-- planned at: it is the only cap that has ever shipped, because the
-- setting is deliberately not exposed in the admin UI (see the comment on
-- audiobookSettingsPayload in internal/handler/audiobook_settings.go).
-- It must stay equal to fileproc.DefaultSegmentChars, which SQL cannot
-- reference — TestSegmentCharsDefaultMatchesTheSplittersDefault holds the
-- two together.
ALTER TABLE book_audiobooks
    ADD COLUMN segment_chars INTEGER NOT NULL DEFAULT 40000;
