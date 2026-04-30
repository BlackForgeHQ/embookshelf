ALTER TABLE libraries ADD COLUMN IF NOT EXISTS file_naming_pattern TEXT;
-- default_naming_pattern row is not restored — the data is irretrievable.
