-- +goose Up

-- The raw text of each compose and .env file, redacted line-for-line so a
-- line diff still shows which line changed without the value being
-- recoverable. Content-addressed, so an unchanged file across a thousand
-- snapshots is stored once.
CREATE TABLE compose_files (
  snapshot_id  INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  path         TEXT NOT NULL,
  content_hash TEXT REFERENCES blobs(hash),   -- NULL unless status = 'ok'
  line_count   INTEGER NOT NULL DEFAULT 0,
  size         INTEGER NOT NULL DEFAULT 0,
  -- ok | unreadable | outside_roots | too_large
  status       TEXT NOT NULL,
  PRIMARY KEY (snapshot_id, path)
);
CREATE INDEX idx_compose_files_path ON compose_files(path, snapshot_id DESC);

-- Manual redaction rules, applied at capture time before anything is written.
-- A hidden value is therefore never stored, rather than stored and later
-- concealed.
--
-- Rules work in both directions. The built-in keep-list is a guess about which
-- keys are safe, and the person running Silt knows better: they can hide what
-- it missed and reveal what it over-hid. Rules beat the keep-list either way.
--
-- Revealing only affects future captures. Earlier snapshots hold a digest, not
-- the value, so there is nothing to retroactively uncover.
CREATE TABLE redaction_rules (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  -- Empty path means the rule applies to every file in the project.
  path       TEXT NOT NULL DEFAULT '',
  -- hide | reveal
  action     TEXT NOT NULL,
  -- key  — match the value of this key wherever it appears in the file.
  --        Stable across edits, which is why it is the preferred form.
  -- line — match this line number. Line numbers shift when a file is edited,
  --        so a `hide` line rule fails closed: if the line moved, it still
  --        hides that line rather than exposing whatever took its place. A
  --        `reveal` line rule fails closed the other way, by lapsing.
  kind       TEXT NOT NULL,
  key        TEXT NOT NULL DEFAULT '',
  line_no    INTEGER NOT NULL DEFAULT 0,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE (project_id, path, action, kind, key, line_no)
);
CREATE INDEX idx_redaction_rules_project ON redaction_rules(project_id);

-- A third fingerprint, for the same reason there are two already: files change
-- independently of what is running. An edited compose file that has not been
-- applied is drift, not a configuration change, and conflating them would
-- report changes that never happened.
ALTER TABLE snapshots ADD COLUMN files_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots ADD COLUMN files_changed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
DROP INDEX idx_redaction_rules_project;
DROP TABLE redaction_rules;
DROP INDEX idx_compose_files_path;
DROP TABLE compose_files;
ALTER TABLE snapshots DROP COLUMN files_changed;
ALTER TABLE snapshots DROP COLUMN files_fingerprint;
