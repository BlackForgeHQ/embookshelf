-- See postgres/000036_kindle_email.up.sql for the rationale.
ALTER TABLE users
    ADD COLUMN kindle_email TEXT NOT NULL DEFAULT '';
