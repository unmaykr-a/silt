-- name: UpsertHost :one
INSERT INTO hosts (name, endpoint, docker_version, last_seen_at, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
  endpoint = excluded.endpoint,
  docker_version = excluded.docker_version,
  last_seen_at = excluded.last_seen_at
RETURNING *;

-- name: ListHosts :many
SELECT * FROM hosts ORDER BY name;
