-- name: PutBlob :exec
INSERT INTO blobs (hash, size, content, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (hash) DO NOTHING;

-- name: GetBlob :one
SELECT content FROM blobs WHERE hash = ?;

-- name: BlobExists :one
SELECT EXISTS (SELECT 1 FROM blobs WHERE hash = ?);

-- CAST is required: sqlc cannot infer a type for SUM/COALESCE on SQLite and
-- emits interface{} without it.
-- name: CountBlobs :one
SELECT
  COUNT(*) AS blob_count,
  CAST(COALESCE(SUM(size), 0) AS INTEGER) AS uncompressed_bytes,
  CAST(COALESCE(SUM(LENGTH(content)), 0) AS INTEGER) AS stored_bytes
FROM blobs;

-- Delete blobs no snapshot or service state references any more. Walking
-- service_states.inspect_hash as well as snapshots.compose_hash is essential:
-- inspect blobs are the majority of rows, and omitting them here would leak
-- the entire store.
-- env_keys are content-addressed too, so they orphan the same way blobs do.
-- name: DeleteUnreferencedEnvKeys :execrows
DELETE FROM env_keys
WHERE inspect_hash NOT IN (SELECT inspect_hash FROM service_states WHERE inspect_hash IS NOT NULL);

-- name: DeleteUnreferencedBlobs :execrows
DELETE FROM blobs
WHERE hash NOT IN (SELECT compose_hash FROM snapshots)
  AND hash NOT IN (SELECT inspect_hash FROM service_states WHERE inspect_hash IS NOT NULL);
