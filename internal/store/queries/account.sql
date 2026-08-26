-- name: GetLocalAccount :one
SELECT id, password_hash, enabled, oidc_subject, created_at, updated_at
FROM local_account
WHERE id = 1;

-- name: CreateLocalAccount :exec
INSERT INTO local_account (id, password_hash, enabled, oidc_subject, created_at, updated_at)
VALUES (1, '', 1, '', ?, ?)
ON CONFLICT (id) DO NOTHING;

-- name: SetLocalPassword :exec
UPDATE local_account SET password_hash = ?, updated_at = ? WHERE id = 1;

-- name: SetLocalEnabled :exec
UPDATE local_account SET enabled = ?, updated_at = ? WHERE id = 1;

-- name: SetLocalOIDCSubject :exec
UPDATE local_account SET oidc_subject = ?, updated_at = ? WHERE id = 1;
