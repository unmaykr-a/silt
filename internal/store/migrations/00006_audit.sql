-- +goose Up

-- What people did to Silt itself.
--
-- Silt records what changed on the host. It did not record what changed *in
-- Silt* — who edited retention, who ran a prune that deleted history, who
-- revoked every session, who signed in and failed. On a single-operator
-- homelab that is a gap you can live with because the answer is always "me".
-- The moment a second person can sign in, it is the first question anyone
-- asks, and the data to answer it does not exist retroactively.
--
-- Deliberately narrow. This is an administrative trail, not request logging:
-- one row per action that changes state or grants access, never one row per
-- page view. A table that grows with reads would be a cost with no reader.
--
-- actor is a display string, not a foreign key. Identity comes from three
-- unrelated places — the built-in account, an OIDC subject, a header from a
-- trusted proxy — and none of them is a row Silt owns. Recording who it *was*
-- at the time is the honest thing; a reference would dangle the moment a
-- provider renamed someone.
CREATE TABLE audit_log (
  id      INTEGER PRIMARY KEY,
  ts      INTEGER NOT NULL,
  -- Empty when Silt acted on its own (scheduled retention, say).
  actor   TEXT NOT NULL DEFAULT '',
  -- How that actor was identified: local, oidc, proxy, or system.
  method  TEXT NOT NULL DEFAULT '',
  action  TEXT NOT NULL,
  -- Whether it worked. A failed sign-in is the row you most want to keep.
  ok      INTEGER NOT NULL DEFAULT 1,
  -- Free-form JSON. Never a secret: what changed, not what it changed to.
  detail  TEXT NOT NULL DEFAULT '{}',
  -- The client address, already narrowed to what the proxy configuration
  -- says is trustworthy.
  remote  TEXT NOT NULL DEFAULT ''
);

-- The screen reads newest-first and retention deletes oldest-first, so one
-- index on ts serves both.
CREATE INDEX audit_log_ts ON audit_log (ts DESC);

-- +goose Down
DROP TABLE audit_log;
