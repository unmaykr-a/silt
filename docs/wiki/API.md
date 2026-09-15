# HTTP API

The contract is [`api/openapi.yaml`](https://github.com/unmaykr-a/silt/blob/main/api/openapi.yaml)
— hand-maintained, and not decorative: `openapi-typescript` generates the frontend's types from
it, and a Go test asserts in both directions that every documented operation is reachable and
shaped as declared, and that every registered route is documented. Drift fails CI rather than
surfacing in a browser.

All timestamps are Unix milliseconds in UTC.

## Authentication

Every `/api/` path needs a session, except the ones that cannot: `POST /api/login`,
`POST /api/auth/setup`, the OIDC round trip, and `POST /api/ingest` (which has its own token).
`/healthz` and `/readyz` are always open.

Unsafe methods under `/api/` additionally need an administrator — see
[Authentication](Authentication#roles).

Requests are CSRF-checked on `Sec-Fetch-Site`/`Origin` before authentication, so a cross-origin
call is refused whether or not authentication is configured.

## Reading the journal

| | |
|---|---|
| `GET /api/hosts` | Docker hosts. |
| `GET /api/projects` | Compose projects. |
| `GET /api/projects/{id}` | One project. |
| `GET /api/projects/{id}/snapshots` | Its snapshots, newest first. `before`, `limit`, `changed_only`. |
| `GET /api/projects/{id}/services` | Service names seen in it. |
| `GET /api/projects/{id}/services/{service}` | One service's history: image identity, state, restarts, env key changes. |
| `GET /api/snapshots/{id}` | One snapshot with its service states. |
| `GET /api/snapshots/{id}/compose` | The effective, redacted project model. `format=json\|yaml`. |
| `GET /api/diff` | Structural diff between two snapshots. |
| `GET /api/timeline` | Changes and events on one axis. `bucket` for the density strip. |
| `GET /api/events` | Events. |
| `GET /api/overview` | The Projects screen's fleet summary. |
| `GET /api/search` | Projects, services, env key **names**, file paths, event text. Never values. |
| `GET /api/audit` | The administrative trail. |

## Compose files

| | |
|---|---|
| `GET /api/projects/{id}/files` | Paths captured for this project. |
| `GET /api/projects/{id}/files/preview` | The current file with per-line redaction reasons. |
| `GET /api/snapshots/{id}/files` | Files captured in a snapshot. |
| `GET /api/snapshots/{id}/file` | One captured file's content. |
| `GET /api/diff/file` | Line diff of one file between two snapshots. |
| `GET/POST/DELETE /api/projects/{id}/redaction-rules` | The hide/reveal decisions. |

## Live updates

`GET /api/stream` — server-sent events. A `ready` event on connect, then `event` and `change`
as they happen, plus a named `heartbeat` every 20 seconds.

The heartbeat is a named event rather than an SSE comment on purpose: a comment keeps proxies
from closing an idle connection, which was the job, but `EventSource` discards it without
telling anyone — so a browser could not distinguish a live connection with nothing happening
from one that had quietly wedged.

A slow client's events are dropped rather than blocking the collector. Live updates are a
convenience; the database is the record.

## Writing

| | |
|---|---|
| `POST /api/ingest` | External events. See [Webhook ingest](Webhook-Ingest). |
| `POST /api/projects/{id}/snapshot` | Snapshot now. |
| `GET/PUT/DELETE /api/settings` | Read, patch, or drop all overrides. |
| `GET /api/settings/export` | The override document as a file. |
| `GET /api/settings/probes` | Live checks: Docker, database, compose roots. |
| `POST /api/settings/notifications/test` | Send a test to every target. |
| `POST /api/maintenance/prune` | Run the retention pass now. |
| `GET /api/maintenance/backup` | A consistent database snapshot. See [Backups](Backups). |
| `GET /api/version` | Version and release. |

`PUT /api/settings` takes a **sparse patch**: only the fields you send become overrides. That is
also the restore path for a settings export, which is why there is no separate import endpoint —
a second write path would be a second set of validation rules to keep in step.

## Accounts and sessions

| | |
|---|---|
| `GET /api/auth` | What this install offers and who you are. |
| `POST /api/login` / `POST /api/logout` | The built-in account. |
| `POST /api/auth/setup` | Claim the account. Works once. |
| `PUT /api/auth/password` | Change it. Revokes other sessions. |
| `PUT /api/auth/account` | Enable or disable the built-in account. |
| `GET /api/auth/login` · `GET /api/auth/callback` | The OIDC round trip. |
| `GET/DELETE /api/auth/link` | Link or unlink a provider identity. |
| `GET/DELETE /api/auth/sessions` | Count, or revoke all. |

## Errors

JSON, `{"error": "..."}`, with the status that matches: `400` malformed, `401` not signed in,
`403` cross-origin or not an administrator, `404` no such thing, `409` already done, `429` rate
limited, `503` not configured.

An unrouted `/api/` path is `404`, and a real one with the wrong method is `405` with the
accepted methods named — not the single-page app with a `200`, which is what it used to be.
