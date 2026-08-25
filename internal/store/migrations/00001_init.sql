-- +goose Up

-- Content-addressed store. Identical content is stored once, so snapshotting
-- 40 services every 5 minutes costs almost nothing when nothing changes.
CREATE TABLE blobs (
  hash        TEXT PRIMARY KEY,
  size        INTEGER NOT NULL,          -- uncompressed size, for accounting
  content     BLOB NOT NULL,             -- zstd-compressed
  created_at  INTEGER NOT NULL           -- unix ms
);

CREATE TABLE hosts (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL UNIQUE,
  endpoint       TEXT NOT NULL,
  docker_version TEXT,
  last_seen_at   INTEGER,
  created_at     INTEGER NOT NULL
);

CREATE TABLE projects (
  id            INTEGER PRIMARY KEY,
  host_id       INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  working_dir   TEXT NOT NULL DEFAULT '',
  config_files  TEXT NOT NULL DEFAULT '[]',   -- JSON array
  first_seen_at INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL,
  archived      INTEGER NOT NULL DEFAULT 0,
  UNIQUE (host_id, name)
);

-- One row per observation of a whole project.
--
-- Two fingerprints, deliberately. A single fingerprint covering config and
-- runtime means a restarting container earns the long retention tier and fires
-- a notification; a crash-looping container would then spam notifications and
-- bloat the database at exactly the moment Silt needs to stay readable.
-- Retention tiering and notifications key off config_changed only.
CREATE TABLE snapshots (
  id                  INTEGER PRIMARY KEY,
  project_id          INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  taken_at            INTEGER NOT NULL,     -- unix ms
  trigger             TEXT NOT NULL,        -- event | file | interval | manual
  compose_hash        TEXT NOT NULL REFERENCES blobs(hash),
  compose_source      TEXT NOT NULL,        -- containers | files | unavailable
  config_fingerprint  TEXT NOT NULL,
  runtime_fingerprint TEXT NOT NULL,
  config_changed      INTEGER NOT NULL,
  runtime_changed     INTEGER NOT NULL,
  -- An observation that matches the previous snapshot exactly carries no
  -- information beyond "still true at T", so it touches these instead of
  -- inserting a row. Writing one would cost a service_states row per service
  -- and an env_keys row per variable — hundreds of rows to record that
  -- nothing happened.
  last_observed_at    INTEGER NOT NULL,
  observation_count   INTEGER NOT NULL DEFAULT 1,
  UNIQUE (project_id, taken_at)
);
CREATE INDEX idx_snapshots_project_time ON snapshots(project_id, taken_at DESC);
CREATE INDEX idx_snapshots_changed ON snapshots(project_id, config_changed, taken_at DESC);

CREATE TABLE service_states (
  id               INTEGER PRIMARY KEY,
  snapshot_id      INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  service          TEXT NOT NULL,
  container_id     TEXT NOT NULL DEFAULT '',
  container_name   TEXT NOT NULL DEFAULT '',
  image_ref        TEXT NOT NULL DEFAULT '',
  -- image_id is the local image config ID and is always present. It is what
  -- the config fingerprint uses. image_digest is the registry digest, which is
  -- empty for locally-built images and is provenance only.
  image_id         TEXT NOT NULL DEFAULT '',
  image_digest     TEXT NOT NULL DEFAULT '',
  image_created_at INTEGER,
  state            TEXT NOT NULL DEFAULT '',
  health           TEXT NOT NULL DEFAULT '',
  restart_count    INTEGER NOT NULL DEFAULT 0,
  started_at       INTEGER,
  inspect_hash     TEXT REFERENCES blobs(hash),
  UNIQUE (snapshot_id, service)
);
-- The Service screen asks for one service's history; without this it scans.
CREATE INDEX idx_service_states_service ON service_states(service, snapshot_id DESC);
CREATE INDEX idx_service_states_snapshot ON service_states(snapshot_id);

-- Env keys are indexed separately so "when did SECRET_KEY last change?" is one
-- query. Values are NEVER stored unless the key is on the keep-list.
-- Keyed by the content address of the service's inspect blob rather than by
-- snapshot, so an unchanged service stores one set of rows however many times
-- it is observed. Reached via service_states.inspect_hash.
CREATE TABLE env_keys (
  inspect_hash     TEXT NOT NULL,
  key              TEXT NOT NULL,
  -- HMAC under a per-install random key, not a bare hash: sha256(value)[:12]
  -- plus an exact length is a brute-force oracle for any low-entropy secret.
  value_hmac       TEXT NOT NULL,
  value_len_bucket TEXT NOT NULL,          -- empty | short | medium | long
  redacted         INTEGER NOT NULL DEFAULT 1,
  value            TEXT,                   -- only when redacted = 0
  PRIMARY KEY (inspect_hash, key)
);
CREATE INDEX idx_env_keys_key ON env_keys(key);

CREATE TABLE events (
  id         INTEGER PRIMARY KEY,
  host_id    INTEGER REFERENCES hosts(id) ON DELETE CASCADE,
  project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
  service    TEXT NOT NULL DEFAULT '',
  ts         INTEGER NOT NULL,             -- unix ms
  source     TEXT NOT NULL,                -- docker | silt | webhook
  type       TEXT NOT NULL,
  severity   TEXT NOT NULL DEFAULT 'info',
  actor      TEXT NOT NULL DEFAULT '',
  message    TEXT NOT NULL DEFAULT '',
  payload    TEXT NOT NULL DEFAULT ''      -- JSON
);
CREATE INDEX idx_events_ts ON events(ts DESC);
CREATE INDEX idx_events_project_ts ON events(project_id, ts DESC);
CREATE INDEX idx_events_type_ts ON events(type, ts DESC);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
DROP TABLE events;
DROP TABLE env_keys;
DROP TABLE service_states;
DROP TABLE snapshots;
DROP TABLE projects;
DROP TABLE hosts;
DROP TABLE blobs;
