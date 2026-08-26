-- name: InsertComposeFile :exec
INSERT INTO compose_files (snapshot_id, path, content_hash, line_count, size, status)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListComposeFiles :many
SELECT * FROM compose_files WHERE snapshot_id = ? ORDER BY path;

-- name: GetComposeFile :one
SELECT * FROM compose_files WHERE snapshot_id = ? AND path = ?;

-- Every distinct path a project has ever had a file at, newest first, so the
-- UI can offer a file even if the latest snapshot could not read it.
-- name: ProjectFilePaths :many
SELECT DISTINCT compose_files.path
FROM compose_files
JOIN snapshots ON snapshots.id = compose_files.snapshot_id
WHERE snapshots.project_id = ?
ORDER BY compose_files.path;

-- Delete compose file rows whose blob nothing else references. Handled by the
-- snapshot cascade, so this exists only for the GC pass to reason about.
-- name: CountComposeFiles :one
SELECT COUNT(*) FROM compose_files;

-- name: ListRedactionRules :many
SELECT * FROM redaction_rules WHERE project_id = ? ORDER BY path, kind, key, line_no;

-- name: InsertRedactionRule :one
INSERT INTO redaction_rules (project_id, path, action, kind, key, line_no, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, path, action, kind, key, line_no) DO UPDATE SET note = excluded.note
RETURNING *;

-- Toggling a line replaces any opposing rule for the same target, so a line
-- cannot end up both hidden and revealed.
-- name: DeleteOpposingRule :execrows
DELETE FROM redaction_rules
WHERE project_id = ? AND path = ? AND kind = ? AND key = ? AND line_no = ? AND action != ?;

-- name: DeleteRedactionRule :execrows
DELETE FROM redaction_rules WHERE id = ? AND project_id = ?;
