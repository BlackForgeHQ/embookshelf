ALTER TABLE libraries ADD COLUMN file_naming_pattern TEXT;
-- default_naming_pattern row is not restored — the data is irretrievable.
