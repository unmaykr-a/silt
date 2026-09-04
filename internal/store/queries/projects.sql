-- name: UpsertProject :one
INSERT INTO projects (host_id, name, working_dir, config_files, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (host_id, name) DO UPDATE SET
  working_dir = excluded.working_dir,
  config_files = excluded.config_files,
  last_seen_at = excluded.last_seen_at,
  archived = 0
RETURNING *;

-- name: ListProjects :many
SELECT * FROM projects WHERE host_id = ? ORDER BY name;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: GetProjectIDByName :one
-- Resolves a compose project name to its id, for the event path.
--
-- Names are unique per host and Silt records one host, so the first match is
-- the answer. Served by the UNIQUE (host_id, name) index; the alternative was
-- listing every host and every project on every Docker event, which on a
-- forty-project host during a `compose up` is a table scan per event.
SELECT p.id FROM projects p
JOIN hosts h ON h.id = p.host_id
WHERE p.name = ?
ORDER BY p.id
LIMIT 1;
