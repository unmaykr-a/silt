# How it works

One binary, one SQLite file, one Docker host. The UI is embedded in the binary.

```
Docker socket proxy (read-only)
        │  events, containers, images
        ▼
   collector ──── coalescer ──── snapshotter ──── SQLite (WAL)
        │            (2s)             │              │
   file watcher                   redactor       zstd blobs
   (fsnotify)                                   content-addressed
                                                    │
                                     HTTP API ◄──────┘
                                        │
                                   SSE ─┴─ embedded Svelte UI
```

## The four triggers

A snapshot is taken when any of these fires:

1. **Docker events** — the `/events` stream, filtered to containers and images. A
   `docker compose up` fires a burst, so events are coalesced per project over a two-second
   window and produce one snapshot.
2. **Compose file changes** — `fsnotify` on the directories holding the files, debounced a
   second. See [Compose file capture](Compose-File-Capture).
3. **Interval reconcile** — every `SILT_SNAPSHOT_INTERVAL` (default 5m), catching anything the
   stream missed.
4. **Manual** — the "Snapshot now" button.

### Noise the stream is filtered for

Every container with a `HEALTHCHECK` emits `exec_create` and `exec_start` on **every probe**.
Forty services on thirty-second healthchecks is roughly 230,000 events a day, all of it noise.
Those are dropped, along with `top`, `attach` and `resize`. Docker appends the command to exec
actions, so the filter is a prefix match rather than equality.

### The reconnect contract

The stream drops — proxies restart, daemons upgrade, networks blip. Without a contract Silt
silently stops recording and nobody notices until 03:10, which is the one moment it needed to
be working. So:

- Reconnect with exponential backoff, 1s → 30s cap, full jitter.
- Resume with `since=<last seen event>` to replay the gap.
- Run a **full reconcile of every project** on every successful reconnect, because replay is
  best-effort and the daemon may have pruned its buffer.
- Emit `silt.stream.disconnected` / `silt.stream.reconnected` so gaps are **visible on the
  timeline** rather than invisible.

## Two sources of truth

**The Docker API is primary.** What is actually running, read from container inspection: image
reference, resolved image ID and digest, environment, mounts, ports, labels, restart policy,
state, health, restart count.

**Compose files on disk are enrichment.** They add what the running state cannot tell you —
which line changed, and whether the file has been edited without being applied.

Deliberately *not* used: `compose-go` interpolation. Loading the files through it and merging
the model would report that an environment key moved, not where in the file it lives or what
sits around it — and a single `${VAR:?}` in one project would abort the load for every project
on the host.

## Three fingerprints

Each snapshot carries three hashes, and they are separate on purpose:

| Fingerprint | Over | Answers |
|---|---|---|
| **config** | the normalised project model | did the configuration change? |
| **runtime** | state, health, restart count, started-at | did it restart or turn unhealthy? |
| **files** | the captured file contents | did someone edit a file? |

A container restarting is a runtime change and not a configuration change. Collapsing them
would mean every restart looked like a change to your stack, and the timeline would be useless
within a day.

## Storage

- **Content-addressed blobs.** Each distinct payload is stored once, zstd-compressed, keyed by
  its hash. Forty services on the same base image share one blob.
- **Touch instead of insert.** An observation identical to the previous one updates that row's
  `last_observed_at` and bumps a counter rather than inserting. An idle hour of five-minute
  snapshots across forty services costs zero bytes.
- **WAL mode, two connection pools.** One writer (a single connection, which makes
  "one writer" structural rather than hoped-for) and a read pool. Timestamps are UTC
  milliseconds; conversion happens in the browser.
- **Pure-Go SQLite** (`modernc.org/sqlite`), so `CGO_ENABLED=0` and the binary is static.

## Diffs

Computed server-side over the normalised models, classified by kind (image ID, image digest,
volumes, ports, environment, labels, service added or removed, …) and given a severity — an
image digest moving is high, a label is low.

Structural or as rendered YAML, side-by-side or unified, with a real unified diff you can
`git apply`. Line diffs over captured files are separate and answer the other question.

## Migrations

Embedded, applied forward at startup with goose. They only ever move forward: there is no down
migration path in a tool whose entire value is a history you cannot reconstruct. Typed queries
come from sqlc, except where SQLite's grammar defeats it — search and the overview are
hand-written SQL, with a comment saying why.

## Where to read more

The design brief, [`PROJECT.md`](https://github.com/unmaykr-a/silt/blob/main/PROJECT.md), has
the full data model, the normalisation rules, and a numbered list of every decision that
changed during implementation and why. That list is the useful half.
