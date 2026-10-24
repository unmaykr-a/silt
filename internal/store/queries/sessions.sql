-- name: CreateSession :exec
INSERT INTO sessions (token_hash, subject, name, method, role, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT token_hash, subject, name, method, role, created_at, last_seen_at, expires_at
FROM sessions
WHERE token_hash = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteSessionsForSubject :execrows
DELETE FROM sessions WHERE subject = ?;

-- Every session, for the "a token has leaked" button. Deliberately not scoped
-- to a subject: that button is pressed because a credential is loose, and on an
-- install with a provider the loose one usually belongs to somebody else.
-- name: DeleteAllSessions :execrows
DELETE FROM sessions;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?;

-- name: CountSessions :one
SELECT COUNT(*) FROM sessions;
