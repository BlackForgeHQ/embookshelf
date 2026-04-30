ALTER TABLE libraries DROP COLUMN IF EXISTS file_naming_pattern;
DELETE FROM app_settings WHERE name = 'DEFAULT_FILE_NAMING_PATTERN';
