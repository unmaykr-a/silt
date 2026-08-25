-- Unchanged snapshots are proof-of-liveness and prune aggressively. The
-- oldest surviving snapshot for a project is never pruned: it is the base for
-- the earliest diff the UI can offer.
-- name: PruneUnchangedSnapshots :execrows
DELETE FROM snapshots
WHERE snapshots.config_changed = 0
  AND snapshots.taken_at < ?
  AND EXISTS (
    SELECT 1 FROM snapshots AS older
    WHERE older.project_id = snapshots.project_id
      AND older.taken_at < snapshots.taken_at
  );

-- name: PruneChangedSnapshots :execrows
DELETE FROM snapshots
WHERE snapshots.taken_at < ?
  AND EXISTS (
    SELECT 1 FROM snapshots AS older
    WHERE older.project_id = snapshots.project_id
      AND older.taken_at < snapshots.taken_at
  );

-- name: PruneEvents :execrows
DELETE FROM events WHERE ts < ?;
