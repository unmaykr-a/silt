-- name: CreateSession :exec
INSERT INTO sessions (token_hash, subject, name, method, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT token_hash, subject, name, method, created_at, last_seen_at, expires_at
FROM sessions
WHERE token_hash = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteSessionsForSubject :execrows
DELETE FROM sessions WHERE subject = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?;

-- name: CountSessions :one
SELECT COUNT(*) FROM sessions;
