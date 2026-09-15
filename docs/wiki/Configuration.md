# Configuration

Environment variables are the baseline an install boots with. Almost all of them can also be
changed on the Settings screen, which stores the change as an override on top of the
environment and applies it immediately — no restart. Every field on that screen says whether
its value is coming from the environment or from there, and offers a way back.

The settings marked **restart** are the ones the settings screen cannot offer, and there are
five of them. The listen address and the database path are read once, by a socket and a file
handle that cannot be swapped underneath a running process. The compose roots are an allowlist
whose entries only mean anything alongside a matching read-only volume mount, so a path typed
into the UI would name a directory the container cannot see. `SILT_SECRET_KEY` is the key that
encrypts what is stored, and a key kept in the database it protects protects nothing.
`SILT_SETTINGS_RESET` is the way back in when a save goes wrong, which means it has to work
without signing in.

Everything else is editable, authentication included — see
[Authentication](Authentication) for what that means and how to recover from a change that
locks you out.

Copy [`.env.example`](https://github.com/unmaykr-a/silt/blob/main/.env.example) and uncomment
what you need. Every setting has a working default, so an empty `.env` is a valid `.env`.

## Process and storage

| Variable | Default | |
|---|---|---|
| `SILT_PORT` | `8375` | Host port to publish. Read by `docker-compose.yml`, not by Silt. |
| `SILT_LISTEN_ADDR` | `:8375` | Address inside the container. **restart** |
| `SILT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `SILT_DB_PATH` | `/data/silt.db` | The SQLite file. **restart** |
| `SILT_DOCKER_HOST` | `tcp://docker-socket-proxy:2375` | Docker API endpoint. A read-only socket proxy, never the socket itself — see [Install](Installation). Changing it redials: the event stream to the old engine ends and reconnects to the new one. |
| `SILT_HOST_NAME` | `local` | What to call this Docker host in the database. Changing it renames the existing host row, so the history moves with the label — unless a host already holds the new name, in which case both are left alone and this one attaches to the existing row. |
| `SILT_SETTINGS_RESET` | `false` | Set it to `true`, recreate the container, and every setting saved from the UI is dropped — the install runs exactly what its environment says. This is the way back in if a saved authentication change locked you out. Unset it afterwards, or the next restart drops your settings again. **restart** |
| `SILT_SECRET_KEY` | *(empty)* | Encrypts the credentials Silt keeps in its own settings row — the ingest token, the notification targets, and any OIDC client secret set from the UI. Without it those are stored in plaintext, and `GET /api/backup` hands out a copy of that file. Any length. **restart** |
| `SILT_BASE_URL` | *(empty)* | Where Silt is reachable from a browser. Links notifications to the change, derives the OIDC callback, and implies a `Secure` cookie when it is `https://`. |

## Collection

| Variable | Default | |
|---|---|---|
| `SILT_SNAPSHOT_INTERVAL` | `5m` | The reconcile sweep that catches whatever the event stream missed. |
| `SILT_COMPOSE_ROOTS` | *(empty)* | Comma-separated absolute paths, mounted read-only, under which compose files may be read and watched. An allowlist, not a hint. **restart** |
| `SILT_MAX_COMPOSE_FILE_BYTES` | `1048576` | Cap on a single captured file. A file over it is recorded as `too_large`; raising the cap makes it readable on its next capture. |
| `SILT_KEEP_KEYS` | *(empty)* | Extra environment keys kept in cleartext. Adds to the built-in safe list; there is no redact-list. See [Secrets and redaction](Secrets-and-Redaction). |

## Retention

Zero means keep forever. See [Retention](Retention) for what each tier is for.

| Variable | Default | |
|---|---|---|
| `SILT_RETENTION_DAYS` | `365` | Snapshots where the configuration changed. |
| `SILT_UNCHANGED_RETENTION_DAYS` | `7` | Runtime-only snapshots — the proof-of-liveness rows between changes. Cannot outlive the changes they sit between. |
| `SILT_EVENT_RETENTION_DAYS` | `90` | Events, which outvolume snapshots by orders of magnitude. |
| `SILT_AUDIT_RETENTION_DAYS` | `730` | The administrative trail. A row per action rather than per observation, so it stays tiny and its whole value is how far back it reaches. |
| `SILT_RETENTION_INTERVAL` | `1h` | How often the retention pass runs. |
| `SILT_VACUUM_INTERVAL` | `0` | `0` disables. `168h` for weekly. Rewrites the whole file, so it belongs on a long cadence. |

## Notifications

See [Notifications](Notifications).

| Variable | Default | |
|---|---|---|
| `SILT_NOTIFY_URLS` | *(empty)* | Comma-separated shoutrrr targets. |
| `SILT_NOTIFY_ON` | `image_id,image_digest,volumes,service_removed` | Change kinds worth interrupting someone for, or `all`. |
| `SILT_NOTIFY_MIN_SEVERITY` | `medium` | ANDed with the kinds: a change must match a listed kind **and** meet this severity. |

## Authentication

See [Authentication](Authentication). These are editable from the settings screen since
1.2.0. Saving one rebuilds the whole gate: the built-in account is re-read, the forward-auth
rules are rebuilt and provider discovery runs again. Sessions survive it, because they are
rows in the database rather than state in the process.

If a change here leaves nobody able to sign in, set `SILT_SETTINGS_RESET=true` and recreate
the container.

| Variable | Default | |
|---|---|---|
| `SILT_LOCAL_ACCOUNT` | `true` | The built-in account. Off for an install that authenticates only through a provider or a proxy. |
| `SILT_PASSWORD_HASH` | *(empty)* | bcrypt. Claims the built-in account before startup, so the first-run window never exists. **restart** |
| `SILT_TRUST_PROXY_AUTH` | `false` | Believe an identity your reverse proxy asserts in a header. |
| `SILT_AUTH_HEADER` | `X-Remote-User` | The identity header. |
| `SILT_AUTH_GROUPS_HEADER` | `X-Remote-Groups` | The group header. Only read when `SILT_ADMIN_GROUPS` is set. |
| `SILT_ADMIN_GROUPS` | *(empty)* | Groups in that header that mean administrator. Unset ⇒ every forward-auth identity is one. |
| `SILT_TRUSTED_PROXIES` | *(empty)* | Addresses or CIDRs whose auth header is believed. **Set this if you use forward auth** — see the warning in [Authentication](Authentication). |
| `SILT_SESSION_TTL` | `720h` | Session lifetime regardless of activity. |
| `SILT_SESSION_IDLE_TTL` | `168h` | Ends an unused session early. `0` disables. |
| `SILT_COOKIE_SECURE` | `auto` | `Secure` on the session cookie. `auto` infers it from the request; `always` never guesses; `never` is for plain HTTP on a trusted network. |

## OpenID Connect

| Variable | Default | |
|---|---|---|
| `SILT_OIDC_ISSUER` | *(empty)* | Enables OIDC. Paste it exactly as your provider prints it, trailing slash included. |
| `SILT_OIDC_CLIENT_ID` | *(empty)* | The registered client. |
| `SILT_OIDC_CLIENT_SECRET` | *(empty)* | Its secret. Reported as set-or-not, never echoed. |
| `SILT_OIDC_REDIRECT_URL` | *(empty)* | Only if it is not `$SILT_BASE_URL/api/auth/callback`. |
| `SILT_OIDC_SCOPES` | `openid,profile,email` | `openid` is always included whether listed or not. |
| `SILT_OIDC_USERNAME_CLAIM` | `preferred_username` | Providers disagree. |
| `SILT_OIDC_GROUPS_CLAIM` | `groups` | Some use `roles`. |
| `SILT_OIDC_ALLOWED_GROUPS` | *(empty)* | Restricts who may sign in. Both allowlists empty admits anyone the provider authenticates. |
| `SILT_OIDC_ALLOWED_USERS` | *(empty)* | The same, by username, email or subject. |
| `SILT_OIDC_ADMIN_GROUPS` | *(empty)* | Groups that mean administrator. Unset ⇒ everyone admitted is one. |
| `SILT_OIDC_ADMIN_TTL` | `12h` | How long a provider-granted administrator role survives without a fresh sign-in. The session keeps working, read-only, after it. `0` disables the lapse. |

## Everything else

| Variable | Default | |
|---|---|---|
| `SILT_INGEST_TOKEN` | *(empty)* | Required for `POST /api/ingest`. Empty means the endpoint returns 503 — unset never means open. |
| `SILT_INGEST_RATE_PER_MINUTE` | `60` | Events accepted per minute from one source address. `0` disables the limit. |
| `SILT_METRICS_PUBLIC` | `false` | `/metrics` without a session. It names every project on the host. |

## Validation

Silt refuses to start on a configuration that cannot work, rather than starting and behaving
oddly. A malformed `SILT_BASE_URL`, an unknown notification kind, an OIDC issuer over plain
HTTP that is not loopback, a keep-key pattern that would keep more than it names, a relative
compose root, a runtime-only retention window longer than the changed one — each of these is a
startup error with a message naming the variable.

Settings that are *legal* and probably not what you meant get a different treatment: the
**Setup** section of the settings screen lists them, with the variables involved. Forward auth
trusted with no proxy list, notifications configured with no base URL so every message links
nowhere, compose roots set but never mounted. Nothing there is an outage; anything actually
wrong still refuses to start.
