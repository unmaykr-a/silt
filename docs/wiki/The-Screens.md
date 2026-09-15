# The screens

Seven, kept boring and fast. [Try them in the live demo](https://unmaykr-a.github.io/silt/) —
the whole UI in your browser against a made-up host.

Dark by default, light available, and a "system" option that follows the device. Every
timestamp shows relative ("3h ago") with the absolute value on hover, or the other way round if
you prefer — that lives in your browser, not in the install, so two people looking at the same
Silt each get their own.

## Timeline

The home screen, and the whole point of the app: **configuration changes and health events share
one axis.** A change and the outage it might explain are only useful side by side.

- A density strip across the top (drag to zoom into a window, double-click to zoom out, hover
  for counts, and the legend doubles as a series filter).
- A filterable feed below it: project, service, change kind, severity, time range.
- A severity bar per row rather than a dot, day headings, and bursts that expand **in place**
  rather than losing your position.
- Live over server-sent events, with a heartbeat — so the status menu can tell you how long ago
  it last heard from Silt, which is the only honest way to show "nothing has changed".

## Projects

The fleet view rather than a directory: what is running, what is unhealthy, what has been
restarting, what was edited and never applied. Every count above the grid is a filter, and
broken stacks sort first by default.

Restart counters **decay**. Docker's never resets, so one blip three months ago would otherwise
pin a stack to the attention list forever.

## Project

One stack: its snapshot list with change markers, the current service table (image, short
digest, state, health, restarts, uptime), and a "compare last two changes" button.

It reports `compose_source` honestly — a badge when the compose portion is unavailable, and a
warning when the files on disk have drifted from what is running.

## Service

One service over time. Image identity history — **when did this image actually change, and to
what** — a restart sparkline, and the points at which each environment key's value changed.

That last one is the payoff of [redaction](Secrets-and-Redaction): it answers "when did
`API_KEY` change?" without ever having held the key.

## Diff

Any two snapshots. Grouped by service, then by change kind, severity-coloured. Structured view
or rendered YAML side-by-side, with a real unified diff you can `git apply`, adjustable context,
and unchanged runs collapsed.

Export as Markdown for an issue, or as a patch.

## Files

The captured compose and `.env` files: full file, line diff between snapshots, and the "what to
hide" view where you click a line to correct the safe-key list in either direction. Each line
says why it was kept or redacted.

## Settings

Nine sections behind a left rail, with a search box — because forty-odd fields is past the point
where "it is in here somewhere" works. The search matches the **environment variable** as well
as the label, since the compose file is where people know these by name.

- **Setup** — a review of the configuration, naming what is legal and probably not what you
  meant, plus live checks that actually ask whether the Docker endpoint answers and whether each
  compose root is mounted.
- **Appearance**, **Collection**, **Retention**, **Notifications**, **Ingest webhook** —
  editable, applied without a restart.
- **Security** — read-only, plus the account controls and "sign out everywhere". Everything
  reported here is the boundary protecting this screen, which is why none of it is editable.
- **Authentication** — what Silt thinks it was told, for the day forward auth is not working.
- **Environment only** — the settings that need a restart.
- **Storage** — usage, backup, settings export, manual prune.

## Search

One box, reachable from anywhere with `/`. Projects, services, environment variable **names**,
compose file paths and event text.

Never values. Searching a secret finds nothing, by construction.

## One vocabulary for state

Running, starting, unhealthy, restarting, crashed, OOM-killed, stopped, paused — each has one
colour and one word, and no screen invents its own. A container someone stopped on purpose is
grey and is not a fault.
