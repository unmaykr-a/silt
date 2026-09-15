# Backups

**Do not copy `silt.db`.**

The database runs in WAL mode, so at any moment the committed state is spread across
`silt.db`, `silt.db-wal` and `silt.db-shm`. A copy of the first one taken while Silt is running
opens cleanly, reports no error, and is **missing whatever had not been checkpointed** — a
failure that surfaces on the day you restore it.

Silt's whole value is a history nobody can reconstruct, so this is the one backup mistake worth
being loud about.

## What to do instead

One endpoint, one consistent file:

```bash
curl -fsSL --cookie "silt_session=$TOKEN" \
  https://silt.example.lan/api/maintenance/backup -o silt-backup.db
```

Or press **Download backup** under Settings → Storage.

It is written with SQLite's `VACUUM INTO`, which runs in a read transaction — so it sees one
consistent snapshot including everything in the write-ahead log — and produces a single
compacted file with no sidecars. It runs on the read pool, so taking a backup does not block
snapshots.

The result is an ordinary SQLite database, not an archive format.

## Restoring

1. Stop Silt.
2. Put the file where `SILT_DB_PATH` points, replacing what is there. Remove any stale
   `silt.db-wal` and `silt.db-shm` beside it.
3. Start Silt. Migrations run forward at startup, so a backup from an older version is fine.

## Permissions

Administrator only. Redaction means the values inside are keyed digests rather than secrets —
that is the point of it — but the file is every project, every captured compose file, the audit
trail and the session table in one download, and "may read the screens" is not the same
permission as "may walk off with the database".

## Automating it

Point whatever already backs up your host at the endpoint. It needs a session cookie, so the
simplest approach on a single-operator install is to run it from a script that signs in first:

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE=https://silt.example.lan
JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

curl -fsS -c "$JAR" -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d "{\"password\":\"$SILT_PASSWORD\"}" >/dev/null

curl -fsSL -b "$JAR" "$BASE/api/maintenance/backup" \
  -o "/backups/silt-$(date -u +%Y%m%d-%H%M%S).db"
```

Behind forward auth, pass whatever header your proxy expects instead of signing in.

## Settings are separate

Settings → Storage → **Download settings** exports the override document — everything set on
the settings screen rather than by the environment — as JSON, restored through the ordinary
settings write.

That is a different thing from a backup and does not include your history. Secrets are left out
of it and **named** in the file, so you know which ones you will have to set again.
