-- +goose Up

-- Browser sessions.
--
-- The previous scheme was a signed cookie carrying only an expiry, with a
-- signing key generated at startup. That had two costs worth removing now that
-- an identity provider can be in play: every restart logged everyone out, and
-- "sign out" only asked the browser to forget a token that stayed valid until
-- it expired. There was also nothing to attribute — one account, no identity.
--
-- An opaque random token recorded here fixes all three. The token itself is
-- never stored: only its SHA-256, so a copy of the database is not a set of
-- working sessions. It needs no HMAC key, because the token carries no claims
-- to forge — everything about the session is read from this row.
CREATE TABLE sessions (
  -- SHA-256 of the token the browser holds, hex encoded.
  token_hash   TEXT PRIMARY KEY,
  -- Stable identifier from the identity provider, or 'local' for the password.
  subject      TEXT NOT NULL,
  -- Display name, if the provider offered one.
  name         TEXT NOT NULL DEFAULT '',
  -- password | oidc. Forward-auth issues no session: the proxy asserts the
  -- identity on every request, so there is nothing for Silt to remember.
  method       TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  -- Absolute expiry, independent of activity.
  expires_at   INTEGER NOT NULL
);

-- Expiry sweeps scan by time, and revoking every session for one person scans
-- by subject.
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_subject ON sessions(subject);

-- +goose Down
DROP TABLE sessions;
