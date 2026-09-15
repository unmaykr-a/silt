# Retention

Four independent windows, because the four kinds of row have wildly different volumes and
wildly different value. Zero means keep forever on all of them.

| Window | Default | What it covers |
|---|---|---|
| `SILT_RETENTION_DAYS` | 365 | Snapshots where the configuration changed. |
| `SILT_UNCHANGED_RETENTION_DAYS` | 7 | Runtime-only snapshots. |
| `SILT_EVENT_RETENTION_DAYS` | 90 | Events. |
| `SILT_AUDIT_RETENTION_DAYS` | 730 | The administrative trail. |

## Why they are separate

**Changed snapshots are the product.** They are what you came for, they are deduplicated
against each other, and a year of them on a forty-service host is small.

**Runtime-only snapshots are proof of liveness** — the rows between changes that say "still
this, still running". They prune aggressively because they are the cheap, disposable half.
Silt refuses a configuration where they outlive the changes they sit between, because that is
almost certainly backwards.

**Events outvolume snapshots by orders of magnitude.** They are not deduplicated, and a busy
host produces a great many. Under a single shared window they would either force the snapshot
history short or fill the disk.

**The audit trail is tiny and its whole value is reach.** A row per administrative action, not
per observation. Inheriting the event tier would throw away the answer to "who changed this"
after a fortnight to save a few kilobytes.

## The pass

Runs every `SILT_RETENTION_INTERVAL` (default `1h`), in one transaction, so a crash cannot
leave snapshots referencing blobs that were already collected. It prunes each tier, then
garbage-collects blobs nothing references any more.

Settings → Storage → **Run retention pass now** does it on demand and reports what went.

## Vacuum

`SILT_VACUUM_INTERVAL` (default `0`, disabled) reclaims free pages. It rewrites the entire
database file, so it belongs on a much longer cadence than pruning — `168h` for weekly is
reasonable.

The time of the last vacuum is **recorded in the database**, so the cadence survives a restart.
An in-memory clock would have meant a weekly vacuum on a host that pulls images nightly became
a nightly full-file rewrite, which is the one thing an SD card does not want done to it.

## What storage actually looks like

Settings → Storage shows blob count, stored bytes, uncompressed bytes and event count.

Because blobs are content-addressed and an unchanged observation touches the previous row
rather than inserting, the numbers are usually much smaller than people expect. An idle hour of
five-minute snapshots across forty services costs zero bytes.
