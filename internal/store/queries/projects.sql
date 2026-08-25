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
