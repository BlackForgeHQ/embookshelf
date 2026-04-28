-- Tear down everything created by 0000_init.up.sql.
-- Order is reverse-creation so FK constraints don't trip.
-- IF EXISTS on every statement for idempotency.
--
-- Note: books_fts virtual table and its triggers are added in Task 6
-- and will be prepended to that task's down migration — they are NOT
-- listed here so this file stays stable.

-- Indexes are dropped implicitly when their table is dropped in SQLite,
-- but we drop them explicitly first for clarity and safety.

DROP INDEX IF EXISTS users_status_idx;
DROP INDEX IF EXISTS users_oidc_identity;
DROP INDEX IF EXISTS idx_users_email;
DROP INDEX IF EXISTS users_email_key;

DROP INDEX IF EXISTS idx_sessions_expires;
DROP INDEX IF EXISTS idx_sessions_user;

DROP INDEX IF EXISTS shelves_user_id_slug_key;

DROP INDEX IF EXISTS idx_shelf_books_book;

DROP INDEX IF EXISTS bookdrop_items_path_key;
DROP INDEX IF EXISTS idx_bookdrop_state;
DROP INDEX IF EXISTS idx_bookdrop_discovered_at;

DROP INDEX IF EXISTS idx_ubp_book;

DROP INDEX IF EXISTS idx_annotations_user_book;
DROP INDEX IF EXISTS idx_annotations_user_recent;

DROP INDEX IF EXISTS idx_reading_sessions_user_started;
DROP INDEX IF EXISTS idx_reading_sessions_user_book_ended;

DROP INDEX IF EXISTS idx_user_devices_user;
DROP INDEX IF EXISTS idx_user_devices_user_name;

DROP INDEX IF EXISTS idx_books_library_id;
DROP INDEX IF EXISTS idx_books_title;
DROP INDEX IF EXISTS idx_books_format;
DROP INDEX IF EXISTS idx_books_path;

DROP INDEX IF EXISTS libraries_slug_key;
DROP INDEX IF EXISTS libraries_path_key;

-- Drop tables in reverse dependency order.
DROP TABLE IF EXISTS app_settings;
DROP TABLE IF EXISTS provider_settings;
DROP TABLE IF EXISTS user_devices;
DROP TABLE IF EXISTS reading_sessions;
DROP TABLE IF EXISTS annotations;
DROP TABLE IF EXISTS user_book_progress;
DROP TABLE IF EXISTS bookdrop_items;
DROP TABLE IF EXISTS shelf_books;
DROP TABLE IF EXISTS shelves;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS books;
DROP TABLE IF EXISTS libraries;
