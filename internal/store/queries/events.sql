-- name: InsertEvent :one
INSERT INTO events (host_id, project_id, service, ts, source, type, severity, actor, message, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListEvents :many
SELECT * FROM events WHERE ts >= ? AND ts <= ? ORDER BY ts DESC LIMIT ?;

-- name: CountEvents :one
SELECT COUNT(*) FROM events;
