# Compose file capture

Off by default. With `SILT_COMPOSE_ROOTS` set, Silt also captures the compose and `.env` files
themselves — which is what lets it show you **which line changed**, and notice an edit you
made and never applied.

```yaml
environment:
  SILT_COMPOSE_ROOTS: /srv,/opt
volumes:
  - /srv:/srv:ro
  - /opt:/opt:ro
```

Mount them **at the same paths they have on the host**: the paths Silt follows come from
container labels, which record where the files live on the host.

## The allowlist

`SILT_COMPOSE_ROOTS` is an allowlist, not a hint.

The paths come from `com.docker.compose.project.config_files`, a container label — and anyone
who can start a container can set a label. Without a root check, a crafted label could point
Silt at any file the process can read. So:

- A path outside the roots is never read, and never watched.
- Symlinks are resolved **before** the check, so a link inside a mounted root cannot reach out
  of it.
- The roots themselves are also resolved, so a root that is *itself* a symlink — `/srv`
  pointing at external storage, which is ordinary on a small host — still works.

When capture is off, or a file is unreadable, or it is over
`SILT_MAX_COMPOSE_FILE_BYTES`, Silt says so honestly with a status on the snapshot rather than
pretending the file was empty.

## The watch

With roots set, Silt watches the directories holding those files. An edit is on the timeline
within about a second of the save.

**The watch is on the directory, not the file.** Atomic-save editors — vim by default, and most
IDEs — write a new file and rename it over the old one, which replaces the inode. A watch on
the file itself goes deaf after the first save *while continuing to report success*, which is
the worst way for a watch to fail.

Writes are debounced, because one save is not one write: a truncate, several writes and a
rename is normal, and that can be four events in a few milliseconds.

Only files a project actually **declares** count. A compose directory also holds data
directories, lock files and editor swap files, and reacting to a container's own writes would
make Silt a source of the churn it is recording.

The watched set is rebuilt periodically from the running projects, independently of the Docker
event stream — a host whose daemon has gone quiet is exactly the host where an unapplied edit
is easiest to forget, and the event stream will never mention it.

If a root is configured but not mounted, the watcher warns and carries on; the interval
reconcile still catches the change, just later. Settings → Setup → **Run checks** reports
whether each root is actually mounted, because a root that was never mounted looks identical to
a project with no files from every other screen.

## Drift: edited but not applied

When a file's content changes but the running configuration does not, that is **drift** —
someone edited the compose file and has not run `docker compose up`. Silt records a
`config.drift` event, severity warn.

That is worth surfacing precisely because nothing has broken yet.

Because an event scrolls off the timeline while the file stays un-applied, the **Projects**
screen also answers whether it is *still* true, by comparing the files on disk against the ones
in place at the last actual change. It is a state, not just a moment.

## Redaction

Captured files go through per-line redaction, keeping structure and comments exactly as
written. See [Secrets and redaction](Secrets-and-Redaction) — including how to click a line to
hide or reveal it, and why revealing only affects future captures.

## What you get

- **Full file view** — the current captured file, syntax-highlighted, with Silt's redaction
  placeholder visibly marked as redacted.
- **Line diff** — between any two snapshots, with adjustable context or the whole file.
- **What to hide** — the marking view, where each line says why it was kept or redacted.
- **Export** — Markdown for an issue, or a unified diff `patch -p1` will take.
