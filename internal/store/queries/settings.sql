-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: PutSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;
