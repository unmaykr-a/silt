# Metrics and health

## Health endpoints

| | |
|---|---|
| `GET /healthz` | Liveness. Returns `ok` if the process is up. |
| `GET /readyz` | Readiness. Returns `ready` when the database is reachable. |

Both stay reachable **whatever the authentication**, so your orchestrator or uptime monitor does
not need a credential. They return plain text and are never cached.

## Prometheus metrics

`GET /metrics`, in the usual exposition format. **Authenticated by default** — it names every
project on this host and counts their changes, and that is not something to hand to anyone who
can reach the port just because a scrape finds a token inconvenient.

Set `SILT_METRICS_PUBLIC=true` if your scrape cannot present a session. The settings screen
reports it as a warning when it is on, so it stays a deliberate choice.

What is exposed: snapshot and event counts, per-project change counts, storage usage, the number
of connected SSE clients, Docker stream connection state, and the usual Go runtime collectors.

It carries **counts and names, not values.** No environment values, redacted or otherwise.

## Scraping it with authentication on

Behind forward auth, let your proxy assert the identity for the scrape path. With the built-in
account, sign in and reuse the session cookie — the same shape as the
[backup script](Backups#automating-it).

## Logs

Structured, via `slog`, to stdout. `SILT_LOG_LEVEL` is `debug`, `info`, `warn` or `error` and is
editable at runtime from the settings screen, so you can turn on debug during an incident
without recreating the container.

`debug` logs the file watcher's decisions and each snapshot's fingerprint comparison, which is
what you want when Silt is not recording something you expected it to.
