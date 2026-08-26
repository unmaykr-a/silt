-- name: InsertSnapshot :one
INSERT INTO snapshots (
  project_id, taken_at, trigger, compose_hash, compose_source,
  config_fingerprint, runtime_fingerprint, config_changed, runtime_changed,
  last_observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- An observation identical to the previous snapshot records that it is still
-- current, rather than inserting hundreds of duplicate child rows.
-- name: TouchSnapshot :exec
UPDATE snapshots
SET last_observed_at = ?, observation_count = observation_count + 1
WHERE id = ?;

-- name: LatestSnapshot :one
SELECT * FROM snapshots WHERE project_id = ? ORDER BY taken_at DESC LIMIT 1;

-- name: GetSnapshot :one
SELECT * FROM snapshots WHERE id = ?;

-- name: ListSnapshots :many
SELECT * FROM snapshots
WHERE project_id = sqlc.arg(project_id)
  AND taken_at < sqlc.arg(before)
  AND (CAST(sqlc.arg(changed_only) AS INTEGER) = 0 OR config_changed = 1)
ORDER BY taken_at DESC
LIMIT sqlc.arg(max_rows);

-- name: CountSnapshots :one
SELECT COUNT(*) FROM snapshots WHERE project_id = ?;

-- name: InsertServiceState :exec
INSERT INTO service_states (
  snapshot_id, service, container_id, container_name, image_ref, image_id,
  image_digest, image_created_at, state, health, restart_count, started_at, inspect_hash
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListServiceStates :many
SELECT * FROM service_states WHERE snapshot_id = ? ORDER BY service;

-- name: InsertEnvKey :exec
INSERT INTO env_keys (inspect_hash, key, value_hmac, value_len_bucket, redacted, value)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (inspect_hash, key) DO NOTHING;

-- name: ListEnvKeys :many
SELECT env_keys.* FROM env_keys
JOIN service_states ON service_states.inspect_hash = env_keys.inspect_hash
WHERE service_states.snapshot_id = ?
ORDER BY service_states.service, env_keys.key;

-- name: SnapshotsInRange :many
SELECT * FROM snapshots
WHERE taken_at >= sqlc.arg(from_ts)
  AND taken_at <= sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = sqlc.arg(project_id))
ORDER BY taken_at DESC
LIMIT sqlc.arg(max_rows);

-- name: LatestChangedSnapshotsBefore :many
SELECT * FROM snapshots
WHERE project_id = sqlc.arg(project_id)
  AND taken_at < sqlc.arg(before)
  AND config_changed = 1
ORDER BY taken_at DESC
LIMIT sqlc.arg(max_rows);

-- Resolve a service name to the project it most recently belonged to, so an
-- external monitor named after a service still lands on the right project.
-- name: ProjectForService :one
SELECT snapshots.project_id FROM service_states
JOIN snapshots ON snapshots.id = service_states.snapshot_id
WHERE service_states.service = ?
ORDER BY snapshots.taken_at DESC
LIMIT 1;
