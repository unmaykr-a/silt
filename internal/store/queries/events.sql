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

-- One event, for the screen that shows everything about it.
-- name: GetEvent :one
SELECT * FROM events WHERE id = ?;

-- The events either side of one, within a window, so a reader can see what
-- else was happening. Scoped to the same project when there is one, because on
-- a forty-project host "everything in the same minute" is mostly noise from
-- stacks that have nothing to do with it.
-- name: EventsAround :many
SELECT * FROM events
WHERE id != sqlc.arg(id)
  AND ts >= sqlc.arg(from_ts)
  AND ts <= sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = sqlc.arg(project_id))
ORDER BY ts ASC
LIMIT sqlc.arg(max_rows);

-- name: CountEvents :one
SELECT COUNT(*) FROM events;

-- name: InsertAudit :exec
INSERT INTO audit_log (ts, actor, method, action, ok, detail, remote)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAudit :many
SELECT * FROM audit_log
WHERE ts < sqlc.arg(before)
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(max_rows);

-- name: CountAudit :one
SELECT COUNT(*) FROM audit_log;

-- name: PruneAudit :execrows
DELETE FROM audit_log WHERE ts < sqlc.arg(cutoff);
