-- name: InsertEvent :one
INSERT INTO events (host_id, project_id, service, ts, source, type, severity, actor, message, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- Filters are optional: passing an empty string or 0 disables that clause,
-- which keeps one query serving the whole /api/events surface.
-- name: ListEvents :many
SELECT * FROM events
WHERE ts >= sqlc.arg(from_ts)
  AND ts <= sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = sqlc.arg(project_id))
  AND (CAST(sqlc.arg(service) AS TEXT) = '' OR service = sqlc.arg(service))
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(severity) AS TEXT) = '' OR severity = sqlc.arg(severity))
ORDER BY ts DESC
LIMIT sqlc.arg(max_rows);

-- name: CountEvents :one
SELECT COUNT(*) FROM events;
