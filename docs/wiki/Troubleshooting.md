# Troubleshooting

Start with **Settings → Setup → Run checks.** It asks rather than infers: does the Docker
endpoint answer, is the database writable, is each compose root actually mounted. Most of what
follows is faster to diagnose from there.

## Nothing is being recorded

**Check the Docker endpoint.** Settings → Setup → Run checks reports it with the elapsed time.
If it fails:

- Is the socket proxy running, and is `SILT_DOCKER_HOST` pointing at it?
- Does the proxy allow what Silt reads? It needs `CONTAINERS`, `IMAGES`, `EVENTS`, `VERSION`,
  `INFO`. With `EVENTS=0` Silt connects, discovers projects on the interval, and never sees a
  live change — which looks like "sometimes works".
- `POST=0` is correct and expected. Silt never writes.

**Check the projects have Compose labels.** Silt discovers projects from
`com.docker.compose.project`. Containers started with plain `docker run` have no such label and
are not projects.

## Compose files are not captured

The status on the snapshot says which of these it is:

- **`outside_roots`** — the file's path is not under `SILT_COMPOSE_ROOTS`. Remember the roots must
  match the **host** paths, because that is what the container labels record.
- **`unreadable`** — the path is inside a root but not mounted into Silt, or not readable. This is
  the common one, and it looks identical to "no files" from every other screen, which is why
  **Run checks** reports per-root mount status.
- **`too_large`** — over `SILT_MAX_COMPOSE_FILE_BYTES`.

A root that is itself a symlink works. A symlink *inside* a root pointing outside it does not,
and that is deliberate.

## An edit is not showing up immediately

The file watch needs `SILT_COMPOSE_ROOTS` set — it is the same allowlist. If the roots are
configured but not mounted, the watcher warns at startup and the interval reconcile catches the
change later instead.

Turn on `SILT_LOG_LEVEL=debug` (editable from the settings screen, no restart) to see the
watcher's decisions: which directories it watched, and which writes it ignored because the
project does not declare that file.

## Every restart looks like a configuration change

It should not — runtime and configuration are separate fingerprints. If a restart really is
producing `config_changed`, something in the model genuinely moved: most often an image tag that
resolves to a new digest, or a `${VAR}` in the compose file that expanded differently.

The diff will say which field. That is what it is for.

## The timeline is drowning in events

Check `SILT_EVENT_RETENTION_DAYS` and the filters. Health-check noise is already filtered —
`exec_create`, `exec_start`, `top`, `attach`, `resize` are dropped — so what is left is real.

If it is one project flapping, the Projects screen surfaces it, and restart counters decay so
that a blip three months ago is not still pinning it there.

## Live updates stop

The status menu in the header says how long ago Silt was last heard from. If it goes quiet:

- **Reverse proxy buffering.** Silt sets `X-Accel-Buffering: no`, which nginx honours, but set
  `proxy_buffering off` and a long `proxy_read_timeout` too. See
  [Install](Installation#behind-a-reverse-proxy).
- **The Docker stream dropped.** Silt reconnects with backoff and records
  `silt.stream.disconnected` / `silt.stream.reconnected` on the timeline, so a gap is visible
  rather than invisible. If it is reconnecting in a loop, the proxy is the thing to look at.

## I am signed out constantly

- `SILT_SESSION_IDLE_TTL` (default 168h) ends an unused session.
- If it is immediate: the session cookie is not coming back. Either the cookie got `Secure` on a
  plain-HTTP install, or it did not get `Secure` on an HTTPS one and something stripped it. Set
  `SILT_COOKIE_SECURE` explicitly rather than letting Silt infer, or set `SILT_BASE_URL` to your
  `https://` address.

## An OIDC login fails

- **Discovery fails at startup.** The issuer string must match your provider's discovery document
  **character for character**, trailing slash included. authentik's ends in a slash.
- **"Your account is not allowed to sign in."** `SILT_OIDC_ALLOWED_GROUPS` or
  `SILT_OIDC_ALLOWED_USERS` excluded you. Check the groups claim name too —
  `SILT_OIDC_GROUPS_CLAIM` defaults to `groups` and some providers use `roles`.
- **Signs in but names nobody.** `SILT_OIDC_USERNAME_CLAIM`; providers disagree.
- **The callback is rejected by the provider.** Register
  `<where Silt is>/api/auth/callback`. If `SILT_BASE_URL` is unset, Silt derives it from the
  request, so behind a proxy it is the public name that ends up in play.

Settings → Authentication shows what Silt thinks it was told, which is the first question when
this is not working, and used to be answerable only by reading the compose file on the host.

## Forward auth lets everyone in

You did not set `SILT_TRUSTED_PROXIES`. The identity header is settable by anything that can
open a socket, so without a trust list "authenticated" means "reached the port". On a shared
Docker network that is every other container on it. Settings → Setup reports this as an **error**.

## Someone is still an administrator after I removed them from the group

Expected, briefly. A provider's groups are read at sign-in, so a demotion takes effect within
`SILT_OIDC_ADMIN_TTL` (default 12h) or at their next sign-in. To make it immediate: Settings →
Security → **Sign out everywhere**.

Forward auth re-reads groups on every request and is never stale.

## `/metrics` returns 401

By design — it names every project on this host. Set `SILT_METRICS_PUBLIC=true` if your scrape
cannot present a session.

## The disk is filling

Settings → Storage shows where it went. Events are the usual answer: they are not deduplicated
and outvolume snapshots by orders of magnitude. Lower `SILT_EVENT_RETENTION_DAYS` and run the
pass.

Snapshots are deduplicated and an unchanged observation costs nothing, so a large snapshot
count is not the same as a large database. If the *file* is much bigger than the reported
stored bytes, it has free pages — set `SILT_VACUUM_INTERVAL` (`168h` is reasonable).

## A restored backup is missing recent data

You copied `silt.db` rather than using the backup endpoint. See [Backups](Backups) — this is the
failure that copy produces, and it does not announce itself.

## Silt refuses to start

Read the error; it names the variable. Silt validates at startup rather than starting and
behaving oddly. Common ones: a malformed `SILT_BASE_URL`, an unknown kind in `SILT_NOTIFY_ON`, an
OIDC issuer over plain HTTP, a `SILT_KEEP_KEYS` pattern that would keep more than it names, a
relative path in `SILT_COMPOSE_ROOTS`, or runtime-only retention longer than the changed window.

## Still stuck

Open an [issue](https://github.com/unmaykr-a/silt/issues) with `SILT_LOG_LEVEL=debug` output and
what Settings → Setup → Run checks says. Redact nothing from the checks output — it carries no
values by design.
