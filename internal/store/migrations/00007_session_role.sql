-- +goose Up
-- What a session may do, not just who it belongs to.
--
-- Silt is read-only against Docker; the split that matters is between reading
-- the journal and changing Silt's own configuration. See PROJECT.md Section 14.
--
-- Stored on the session rather than looked up per request because the answer
-- comes from the provider's groups at sign-in, and asking the provider again
-- on every request would put an outage at the identity provider between a
-- reader and a page they are allowed to read.
--
-- Defaults to admin so an existing session keeps working across the upgrade:
-- everyone who could sign in before this migration could change everything,
-- and silently demoting them would look like Silt breaking.
ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';

-- +goose Down
ALTER TABLE sessions DROP COLUMN role;
