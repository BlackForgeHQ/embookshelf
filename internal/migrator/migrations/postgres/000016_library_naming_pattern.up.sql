ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS file_naming_pattern TEXT;
