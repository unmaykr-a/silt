# Silt — Project Brief

> **Silt** records what settles on your Docker stack, layer by layer — so when something
> breaks at 03:10 you can see the image that got pulled at 03:00.

This document is the complete starting brief and the source of truth for v1 scope.
Section 15 lists what changed from the first draft and why; read it if you are coming
from that version.

---

## 0. Instructions for Claude Code

Read this whole document before writing any code.

- Work **milestone by milestone** (Section 11). Stop after each milestone and summarise
  what was built and what you'd do next. Do not race ahead to M6.
- Every milestone must end with the repo in a state where `make check` passes
  (`go build ./... && go vet ./... && go test ./... && npm run build`).
- No `TODO` stubs left behind in merged code. If something is out of scope, delete it
  and note it in the milestone summary instead.
- Write tests for: compose normalisation, redaction, the diff engine, and retention
  pruning. Those four are where correctness actually lives. UI can go untested for v1.
  The redaction sentinel test (Section 7) is not optional and must run in CI.
- The tech choices in Section 6 are **locked**. If you believe one is wrong, say so in
  your summary and wait — do not substitute silently.
- Prefer stdlib. Every added dependency must earn its place.

---

## 1. What Silt is

A self-hosted change journal for Docker Compose stacks.

It watches one or more Docker hosts and records, over time:

- the **effective** Compose configuration of every project — reconstructed from what is
  actually running, and enriched from the on-disk compose files when they are readable —
  with secret values redacted
- the **resolved image identity** of every running service: the local image ID and, when
  available, the registry digest — not just the tag
- **container state**: id, state, health, restart count, started-at, and a normalised
  subset of `docker inspect`
- a stream of **events**: container start/stop/die/health transitions, image pulls,
  detected config changes, plus anything external POSTed to its ingest endpoint

Then it lets you answer one question well: **what changed, and when?** Pick any two
snapshots of a project and see a structured diff. Scan a timeline and see change markers
sitting next to health events.

### Why it should exist

- Enterprise SaaS has this ("Change Tracking" in Datadog and New Relic). Closed, hosted,
  per-host pricing.
- Kubernetes has this (Argo CD, Flux — history, diff, rollback, health).
- Compose has **nothing**. Portainer Business Edition has stack versioning; Portainer CE
  has none, and the BE implementation is widely disliked because it versions the
  directory path and breaks relative bind mounts.
- Dockge and Portainer CE keep no history. Komodo logs actions but not browsable,
  diffable config history. Diun and What's-Up-Docker only tell you a new tag exists.
  Uptime Kuma knows something went down but has no idea why.

Silt is the missing join between "what changed" and "what broke".

### Non-goals (v1 and probably ever)

- Not a deployment tool. Silt **never writes to the Docker API.** It observes. This is a
  hard architectural rule, not a v1 shortcut — it's what makes it safe to run and easy to
  trust. Rollback, if it ever exists, means "here is the old compose file, go apply it
  yourself".
- Not a monitoring system. It consumes health signals; it does not probe.
- Not a log aggregator. Container logs are out of scope.
- Not Kubernetes. Compose only.
- No `linux/arm/v7`. `linux/amd64` and `linux/arm64` only — say so in the README.

---

## 2. v1 scope

**In:**

- Single Docker host, reached over TCP through a socket proxy
- Automatic project discovery from Compose container labels
- Snapshots on: docker events, compose file changes, a periodic reconcile, manual trigger
- Content-addressed snapshot storage in SQLite
- Structural diff between any two snapshots, classified by change kind
- Timeline UI with live updates over SSE
- Notifications via shoutrrr on significant changes
- Generic webhook ingest so Uptime Kuma / Home Assistant / anything can post events
- Retention and pruning
- Multi-arch container image, single binary, UI embedded

**Deferred to v2+:**

- Multi-host via a separate lightweight agent binary (but see Section 5 — design for it now)
- Automatic correlation ("these 3 changes preceded this outage") — v1 just puts both on
  one timeline and lets the human do the correlating
- OIDC login (v1 uses forward-auth headers)
- Prometheus metrics beyond a basic `/metrics`

---

## 3. Architecture

```
┌──────────────────────────────────┐
│ docker-socket-proxy              │  tecnativa/docker-socket-proxy
│ CONTAINERS=1 IMAGES=1 EVENTS=1   │  POST=0  ← read-only, enforced at the proxy
│ VERSION=1 PING=1 POST=0          │
└──────────┬───────────────────────┘
           │ HTTP (tcp://docker-socket-proxy:2375)
┌──────────▼───────────────────────────────────────────┐
│ silt                                                  │
│                                                       │
│  collect/  ── discovery, event stream, fsnotify,     │
│               snapshot builder                        │
│       │                                               │
│  compose/ ── project model → normalise → redact      │
│       │                                               │
│  store/   ── SQLite (WAL) + content-addressed blobs   │
│       │                                               │
│  diff/    ── structural diff + change classification  │
│       │                                               │
│  api/     ── REST + SSE + ingest webhook              │
│       │                                               │
│  web/     ── embedded Svelte SPA (embed.FS)           │
└───────────────────────────────────────────────────────┘
```

One process, one SQLite file, one port. The UI is compiled into the binary.

### Why a socket proxy and not the socket

Mounting `/var/run/docker.sock:ro` is **not** a security boundary — read-only applies to
the file, not to the API, so anything holding it can still create privileged containers.
The socket proxy enforces read-only at the HTTP verb level. It also means Silt talks TCP,
so the container can run as a non-root user with no docker group membership, which the
distroless `nonroot` base requires anyway.

Ship the proxy in the example `docker-compose.yml`. Make it the documented default.

**Required proxy permissions.** `CONTAINERS=1` (list + inspect), `IMAGES=1` (image
inspect, for digest resolution), `EVENTS=1` (the event stream), `VERSION=1` and `PING=1`
(the Docker Go client negotiates an API version against `/version` and probes `/_ping`
on connect — without these, every call fails with a confusing 403 before Silt has done
anything). `POST=0` is what makes the whole thing safe; state it in the README as the
line users should not change.

Alternatively pin the API version with `client.WithVersion("1.44")` and skip negotiation,
but keep `VERSION=1` anyway so `/version` still populates `hosts.docker_version`.

---

## 4. Data model

SQLite, WAL mode, `busy_timeout=5000`, foreign keys on.

**All timestamps are Unix milliseconds in UTC.** Not seconds. Two reasons: an
event-triggered snapshot and the interval reconcile routinely land inside the same
second, which collides with `UNIQUE (project_id, taken_at)`; and JavaScript's `Date`
takes milliseconds natively, so the frontend never multiplies by 1000. Never store local
time.

```sql
-- Content-addressed store. Identical content is stored once, so snapshotting
-- 40 services every 5 minutes costs almost nothing when nothing changes.
CREATE TABLE blobs (
  hash        TEXT PRIMARY KEY,            -- sha256 hex of the uncompressed content
  size        INTEGER NOT NULL,
  content     BLOB NOT NULL,               -- zstd-compressed
  created_at  INTEGER NOT NULL
);

CREATE TABLE hosts (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL UNIQUE,
  endpoint       TEXT NOT NULL,            -- tcp://docker-socket-proxy:2375
  docker_version TEXT,
  last_seen_at   INTEGER,
  created_at     INTEGER NOT NULL
);

CREATE TABLE projects (
  id            INTEGER PRIMARY KEY,
  host_id       INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,             -- com.docker.compose.project
  working_dir   TEXT,                      -- com.docker.compose.project.working_dir
  config_files  TEXT,                      -- JSON array; from .project.config_files
  first_seen_at INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL,
  archived      INTEGER NOT NULL DEFAULT 0,
  UNIQUE (host_id, name)
);

-- One row per observation of a whole project.
CREATE TABLE snapshots (
  id             INTEGER PRIMARY KEY,
  project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  taken_at       INTEGER NOT NULL,         -- unix ms
  trigger        TEXT NOT NULL,            -- event | file | interval | manual
  compose_hash   TEXT NOT NULL REFERENCES blobs(hash),
                                           -- canonical JSON of the effective, redacted
                                           -- project model
  compose_source TEXT NOT NULL,            -- containers | files | unavailable

  -- Two fingerprints, deliberately. See "Why two fingerprints" below.
  config_fingerprint  TEXT NOT NULL,       -- compose_hash + per-service image identity
                                           --   + inspect_hash
  runtime_fingerprint TEXT NOT NULL,       -- per-service state, health, restart_count,
                                           --   started_at
  config_changed  INTEGER NOT NULL,        -- 1 if config_fingerprint  != previous
  runtime_changed INTEGER NOT NULL,        -- 1 if runtime_fingerprint != previous

  UNIQUE (project_id, taken_at)
);
CREATE INDEX idx_snapshots_project_time ON snapshots(project_id, taken_at DESC);
CREATE INDEX idx_snapshots_changed
  ON snapshots(project_id, config_changed, taken_at DESC);

CREATE TABLE service_states (
  id               INTEGER PRIMARY KEY,
  snapshot_id      INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  service          TEXT NOT NULL,          -- com.docker.compose.service
  container_id     TEXT,
  container_name   TEXT,
  image_ref        TEXT,                   -- lscr.io/linuxserver/radarr:latest
  image_id         TEXT,                   -- sha256:... local image config ID.
                                           --   ALWAYS present. This is the identity
                                           --   the config fingerprint uses.
  image_digest     TEXT,                   -- sha256:... registry digest, when known.
                                           --   Empty for locally-built images.
  image_created_at INTEGER,
  state            TEXT,                   -- running | exited | restarting | ...
  health           TEXT,                   -- healthy | unhealthy | starting | none
  restart_count    INTEGER,
  started_at       INTEGER,
  inspect_hash     TEXT REFERENCES blobs(hash),  -- normalised, redacted inspect subset
  UNIQUE (snapshot_id, service)
);
-- The Service screen asks "show me this service's history"; without this it table-scans.
CREATE INDEX idx_service_states_service ON service_states(service, snapshot_id DESC);

-- Env keys are indexed separately so "when did SECRET_KEY last change?" is one query.
-- Values are NEVER stored unless the key is on the keep-list. See Section 7.
-- Keyed by the content address of the service's inspect blob, not by
-- snapshot, so an unchanged service stores one set of rows however many times
-- it is observed. Reached through service_states.inspect_hash.
CREATE TABLE env_keys (
  inspect_hash     TEXT NOT NULL,
  key              TEXT NOT NULL,
  value_hmac       TEXT NOT NULL,          -- first 12 hex of HMAC-SHA256(install_key,
                                           --   value). Comparable only within this
                                           --   install. See Section 7.
  value_len_bucket TEXT NOT NULL,          -- empty | short | medium | long
  redacted         INTEGER NOT NULL DEFAULT 1,  -- 0 only if the key is on the keep-list
  value            TEXT,                   -- only populated when redacted = 0
  PRIMARY KEY (inspect_hash, key)
);
-- Powers "when did this key last change?" across a project's history.
CREATE INDEX idx_env_keys_key ON env_keys(key);

CREATE TABLE events (
  id         INTEGER PRIMARY KEY,
  host_id    INTEGER REFERENCES hosts(id) ON DELETE CASCADE,
  project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
  service    TEXT,
  ts         INTEGER NOT NULL,             -- unix ms
  source     TEXT NOT NULL,                -- docker | silt | webhook
  type       TEXT NOT NULL,                -- container.start, container.die,
                                           -- container.health, image.pull,
                                           -- snapshot.changed, external.down, ...
  severity   TEXT NOT NULL DEFAULT 'info', -- info | warn | error
  actor      TEXT,
  message    TEXT,
  payload    TEXT                          -- JSON
);
CREATE INDEX idx_events_ts         ON events(ts DESC);
CREATE INDEX idx_events_project_ts ON events(project_id, ts DESC);
CREATE INDEX idx_events_type_ts    ON events(type, ts DESC);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- settings holds, among other things, `redaction_hmac_key`: 32 random bytes generated
-- on first boot, base64-encoded. It never leaves the database and is never logged.
```

### Why two fingerprints

A single fingerprint covering both config and runtime state means a container restarting
sets `changed = 1`. That snapshot then earns the 365-day retention tier *and* fires a
notification. A container in a crash-restart loop — exactly when you most want Silt
readable — would spam your ntfy and bloat the database with hundreds of near-identical
snapshots.

So: `config_fingerprint` covers the compose blob, each service's `image_id`, and each
service's `inspect_hash`. `runtime_fingerprint` covers state, health, restart count and
started-at. **Retention tiering and notifications key off `config_changed` only.**
Runtime transitions are already recorded as `events`, which is where they belong on the
timeline; `runtime_changed` exists so the UI can render a marker without diffing, not to
drive policy.

`inspect_hash` must therefore cover only the *configuration* half of `docker inspect`
(`Config.*`, `HostConfig.*`, `NetworkSettings` topology) and must exclude the volatile
half (`State.*`, `RestartCount`, timestamps) — otherwise the split is defeated at source.
Enforce this with an explicit allowlist of inspect fields, not a denylist.

### Observations that change nothing

An observation whose fingerprints both match the previous snapshot carries no
information beyond "still true at T". Inserting a row for it would write a
`service_states` row per service and an `env_keys` row per variable — for 40
services with a dozen variables each, roughly 500 rows to record that nothing
happened. Measured, an idle hour of five-minute snapshots cost ~670 KB that
way, against a 50 KB budget, because blob dedupe cannot touch relational rows.

So such an observation updates `last_observed_at` and `observation_count` on
the existing snapshot and inserts nothing. Measured again, an idle hour now
costs **zero bytes**.

This changes what `config_changed = 0` means. It no longer includes "nothing
happened" — those rows do not exist. It means "runtime changed but
configuration did not": a restart, a health transition. Those are exactly the
rows a crash-looping container produces in bulk, so the short retention tier
still has the right thing to prune.

### Retention policy

- Snapshots with `config_changed = 1` are the valuable ones and they're tiny thanks to
  blob dedupe — keep for `SILT_RETENTION_DAYS` (default 365).
- Snapshots with `config_changed = 0` are runtime-only changes (restarts,
  health transitions) — prune after `SILT_UNCHANGED_RETENTION_DAYS` (default 7).
- Events get their own `SILT_EVENT_RETENTION_DAYS` (default 90). Event volume exceeds
  snapshot volume by orders of magnitude; tying them to the 365-day snapshot tier is how
  the database gets to a gigabyte on a Raspberry Pi.
- **Never prune a project's oldest surviving snapshot**, whatever its age or changed
  flag. It is the base for the earliest diff the UI can offer; delete it and the oldest
  visible change has nothing to compare against.

Run a GC pass after pruning that deletes unreferenced blobs **and unreferenced
`env_keys` rows**, which are content-addressed and orphan the same way.
"Unreferenced" means referenced by neither `snapshots.compose_hash` **nor**
`service_states.inspect_hash` —
inspect blobs are the majority of the rows and forgetting them either leaks the whole
store or deletes it, depending on which way you get it wrong. Do the whole pass in one
transaction. `VACUUM` on a much longer cadence (`SILT_VACUUM_INTERVAL`, off by default).

---

## 5. Collection

### Discovery

Do **not** ask the user to configure paths. Enumerate containers and read the Compose
labels Docker Compose writes automatically:

| Label | Use |
|---|---|
| `com.docker.compose.project` | project name |
| `com.docker.compose.service` | service name |
| `com.docker.compose.project.working_dir` | where to look for compose files |
| `com.docker.compose.project.config_files` | the actual file list, comma-separated |
| `com.docker.compose.config-hash` | Compose's own per-service config hash — cheap change hint |

### Two sources of truth, in priority order

This is the most important design decision in the collector, and the original draft had
it backwards.

**1. Containers (primary, always available).** Everything Silt needs is recoverable from
`docker inspect` plus the compose labels: image, env, ports, volumes, networks, labels,
healthcheck, restart policy, command, entrypoint, resource limits. This path needs no
mounts, no file access, and no guessing. It describes **what is actually running**, which
is the question Silt exists to answer. `compose_source = 'containers'`.

**2. Compose files on disk (secondary, enrichment).** When `SILT_COMPOSE_ROOTS` makes the
files readable, load them with `compose-go` to recover what containers cannot show:
top-level `volumes:`/`networks:`/`secrets:`/`configs:` declarations, `profiles`, service
definitions for containers that are *not currently running*, and the file layering
(`include`, `extends`). Merge this over the container-derived model.
`compose_source = 'files'`.

**3. Neither.** Mark `compose_source = 'unavailable'` and snapshot service state only.

Two hazards make the file path unsuitable as the primary source, and both must be handled
even when it is used as enrichment:

- **Interpolation needs an environment Silt does not have.** `compose-go` resolves
  `${VAR}` at load time against the process environment plus the adjacent `.env`. Silt's
  process environment is not the operator's shell environment. So values will render
  empty or wrong, and — worse — `${VAR:?message}` makes the entire load **fail**. Load
  with interpolation errors downgraded to warnings and missing variables resolving to
  empty; never let one stack using `:?` take down collection for every project on the
  host. Record which variables failed to resolve in the snapshot so the UI can say
  "rendered with 3 unresolved variables" rather than quietly lying.
- **The file may not be what's running.** Someone edited the compose file at 02:00 and
  hasn't run `up` yet. If files were primary, Silt would report a change that never
  happened — the "noise generator" failure mode Section 12 warns about. When the
  container-derived model and the file-derived model disagree, the container wins and the
  divergence is itself worth surfacing: emit a `config.drift` event. That is a genuinely
  useful signal ("your compose file no longer matches your running stack") that falls out
  of this design for free.

Document that mounting the compose roots read-only unlocks full fidelity, and treat the
`unavailable` path as the *normal* path in tests, not the edge case — most users will not
mount anything.

### Triggers

1. **Docker event stream** — subscribe to `/events` filtered to `type=container` and
   `type=image`. Debounce: a `docker compose up` fires a burst, so coalesce events per
   project over a 2-second window and take one snapshot.

   **Filter out `exec_create` and `exec_start`.** Every container with a `HEALTHCHECK`
   emits both on *every probe*. Forty services on 30-second healthchecks is roughly
   230,000 events per day, all of it noise, and under the old shared retention all of it
   kept for a year. Filter at the subscription where possible and again on ingest.
   Also drop `container.top`, `container.attach`, and `container.resize`.

   **Reconnect contract.** The stream drops — proxy restarts, daemon upgrades, network
   blips. Without a contract Silt silently stops recording and nobody notices until 03:10,
   which is the one moment it needed to be working. On disconnect: reconnect with
   exponential backoff (1s → 30s cap, full jitter), resume with `since=<last-seen event
   ts>` to replay the gap, and run a **full reconcile of every project** on every
   successful reconnect, because replay is best-effort and the daemon may have pruned its
   buffer. Emit a `silt.stream.disconnected` / `silt.stream.reconnected` event pair so
   gaps are visible on the timeline rather than invisible.

2. **File watch** — `fsnotify` on the discovered compose/`.env` file paths, where
   readable. Debounce 1s (editors write in several syscalls). Watch the parent directory,
   not the file: atomic-save editors replace the inode and a file-level watch goes deaf
   after the first save.

3. **Interval reconcile** — every `SILT_SNAPSHOT_INTERVAL` (default 5m), catch anything
   missed.

4. **Manual** — `POST /api/projects/{id}/snapshot`.

### Building a snapshot

1. Build the effective project model from containers; enrich from files if available
   (see above). Set `compose_source`.
2. **Normalise**: sort every map into deterministic key order, sort slices whose order is
   not semantically meaningful (`ports`, `volumes`, `networks`, `labels`, `env_file`),
   drop non-deterministic internal fields. Marshal to canonical JSON.
   *Skipping this step is the single biggest way to make Silt useless* — unnormalised
   output makes every key reorder look like a change and the tool becomes noise.
3. **Redact** (Section 7) — after interpolation, not before.
4. Inspect each container; extract the normalised config subset (allowlisted fields
   only, per Section 4).
5. Resolve image identity: `ImageInspect.ID` always, into `image_id`. For `image_digest`,
   read `RepoDigests` — but it is **empty for locally-built images** (`build:` in
   compose) and can hold **several entries** when an image is tagged across registries,
   so match the entry whose repository matches the container's image ref rather than
   taking `[0]`. Never fingerprint on `image_digest`; it is display and pull provenance.
   `image_id` is the identity.
6. Compute both fingerprints, compare to the previous snapshot, set `config_changed` and
   `runtime_changed`.
7. If `config_changed`, write a `snapshot.changed` event and fire notifications if the
   change kinds and severity match `SILT_NOTIFY_ON` / `SILT_NOTIFY_MIN_SEVERITY`.

### Designing for multi-host now

`host_id` is on every table that needs it, so the schema is ready. Two rules keep v2
cheap: the collector is an **interface** from day one (`Collector` with
`Discover`/`Inspect`/`Events`), with the local Docker client as its only v1
implementation; and no API handler or query may assume `host_id = 1`.

---

## 6. Tech stack — locked

### Backend

| Concern | Choice | Note |
|---|---|---|
| Language | **Go 1.25+** | Floor set by docker/docker v28, which pulls in OpenTelemetry |
| HTTP router | `net/http` stdlib | 1.22+ method+path patterns are enough |
| Docker | `github.com/docker/docker/client` | |
| Compose parsing | `github.com/compose-spec/compose-go/v2` | the library Compose itself uses |
| Database | `modernc.org/sqlite` | **pure Go — no cgo.** Non-negotiable, see Section 12 |
| Queries | `sqlc` | type-safe Go generated from plain SQL |
| Migrations | `goose` (embedded) | |
| File watching | `fsnotify/fsnotify` | |
| Notifications | `containrrr/shoutrrr` | ntfy, Gotify, Discord, email — one library |
| Compression | `klauspost/compress/zstd` | for blobs |
| Config | `caarlos0/env` | env-var driven, as self-hosters expect |
| Logging | `log/slog` stdlib | |

`sqlc` covers the common cases well but its SQLite support has rough edges; keep a
hand-written `database/sql` escape hatch for the two or three queries it can't express
(the timeline merge is the likely candidate) rather than contorting the schema to suit
the generator.

### Frontend

| Concern | Choice | Note |
|---|---|---|
| Build | **Vite** | |
| Framework | **Svelte 5 + TypeScript** | same shape as Beszel and Pocket-ID |
| Styling | **Tailwind + shadcn-svelte** | dark mode and a component set, no runtime dep |
| Charts | **uPlot** | ~45 KB, canvas. Do NOT pull in D3 or Chart.js |
| Live updates | **SSE** (`EventSource`) | not WebSockets — see Section 12 |
| API types | `openapi-typescript` from `api/openapi.yaml` | catches drift in CI |

**`api/openapi.yaml` is hand-maintained.** There is no spec generator for stdlib
`net/http` handlers, and adding one is not worth a dependency at this size. The spec is
the contract: `openapi-typescript` generates the frontend types from it, and a Go
contract test asserts each handler's response shape matches the spec so drift fails CI
instead of surfacing in the browser.

Output goes to `web/dist`, embedded with `embed.FS` and served from the Go binary.

### Diffs are computed server-side

Normalise both snapshots' project models, structurally diff them, classify each change,
and send JSON hunks. The browser renders; it does not compute. Keeps the client dumb and
avoids shipping a diff engine to every page load.

---

## 7. Redaction — read this twice

**Silt must never persist a recoverable secret.** This is the sentence the project lives
or dies on. A tool that reads every `.env` on someone's box gets exactly one chance to be
trusted. The threat model is explicitly *someone obtains `silt.db`* — a leaked backup, a
misconfigured volume, a shared debug bundle.

The trap: `compose-go` *interpolates* `${VAR}` during load, and `docker inspect` returns
`Config.Env` fully resolved, so both models **contain real secret values**. Redaction
happens **immediately after** the model is built, before anything touches a blob, a
query, or a log line.

### Keep-list, not redact-list

The naive design — "redact if the key matches a secret-ish regex, otherwise keep the
value if it looks harmless" — **fails open**. `PW=hunter2` matches no regex containing
`pass|secret|token`, is short, printable and non-entropic, and would be written to disk
in cleartext. So would `SMTP_LOGIN`, `ADMIN_EMAIL`, `MYSQL_USER`. Every such default is
one key name you didn't think of away from a breach.

So invert it. **Redact everything by default.** Keep cleartext only for keys on an
explicit known-safe list:

```
PUID, PGID, UID, GID, TZ, UMASK, LANG, LANGUAGE, LC_*, TERM, PATH, HOME, HOSTNAME,
NODE_ENV, RAILS_ENV, APP_ENV, ENVIRONMENT, LOG_LEVEL, DEBUG, VERBOSE,
*_PORT, PORT, *_TIMEOUT, *_INTERVAL, *_RETRIES, *_MAX_*, *_MIN_*,
PYTHONUNBUFFERED, GOMAXPROCS, JAVA_OPTS, TERM
```

extended by `SILT_KEEP_KEYS` (comma-separated, `*` glob supported). This still gives you
readable `PUID=1000`, which was the entire motivating example, and it removes the whole
class of "the regex didn't catch it" bugs. There is no `SILT_REDACT_KEYS`; there is
nothing to get wrong.

### Hashing that isn't a cleartext oracle

Storing `sha256(value)[:12]` plus the exact `value_len` is not redaction — it is a
guessing oracle. A four-digit PIN is ten thousand hashes. `hunter2` is in every wordlist.
A known enum (`true`, `admin`, a username) falls on the first try. Exact length narrows
the search for free and buys the UI nothing.

So:

- Generate a random 32-byte `redaction_hmac_key` on first boot; store it in `settings`.
  Hash with **`HMAC-SHA256(install_key, value)`**, keep the first 12 hex characters.
  Hashes stay comparable *within one install* — which is all the "did this value change
  between snapshots?" query ever needs — and are useless to anyone holding the database
  without the key. Never log the key; never expose it over the API.
- Store `value_len_bucket`, not the length: `empty` (0), `short` (1–8), `medium` (9–32),
  `long` (33+).
- Placeholder in the stored compose blob is ASCII: `[redacted:a1b2c3d4e5f6]`. The original
  draft's `«sha256:…»` guillemets are non-ASCII inside a value that gets re-rendered as
  YAML and pasted into shells; don't.

### The rest of the rules

- Every `environment` value not on the keep-list is replaced with the placeholder in the
  stored compose blob. `env_keys` stores key, HMAC prefix, and length bucket.
- Compose top-level `secrets:` and `configs:` — record names and mount targets only,
  never file contents.
- The same redaction runs over the `docker inspect` subset before it becomes a blob.
  `Config.Env` is the obvious one; also scrub `Config.Cmd`, `Config.Entrypoint`
  and `Config.Labels`.
- **Bind mount source paths are redacted**; type, target, mode, and named-volume
  names are kept. A host path can embed a credential and there is no key to judge
  it by, so the choice was between keeping every path, redacting every path, or an
  entropy heuristic — and a heuristic that cannot tell `storage-2023-archive-01`
  from a hex token is a coin flip dressed up as a guarantee. The cost is real: a
  volumes diff says the source of a bind changed without saying what to. Structural
  label namespaces (`com.docker.compose.*`, `org.opencontainers.image.*`) are kept,
  since discovery depends on them and they are definitionally public.
- Redaction runs before `slog` sees anything. Route all model logging through a helper
  that takes an already-redacted model; make it impossible to log the raw one.
- **Sentinel test, required in CI.** Feed a project whose every secret-shaped field
  contains a known sentinel string. Build a snapshot, write it, prune, GC. Then assert the
  sentinel appears nowhere in: the raw `.db` file bytes, the WAL, any blob after zstd
  decompression, captured `slog` output at debug level, and every API response body.
  Byte-scan the file — do not query for it.

Put a short "What Silt stores" section at the top of the README stating this plainly.

---

## 8. HTTP API

```
GET  /api/hosts
GET  /api/projects?host={id}
GET  /api/projects/{id}
GET  /api/projects/{id}/snapshots?before={ts}&limit=50&changed_only=true
POST /api/projects/{id}/snapshot           -- force one now
GET  /api/snapshots/{id}
GET  /api/snapshots/{id}/compose           -- effective, redacted; ?format=yaml|json
GET  /api/diff?from={id}&to={id}
GET  /api/events?from=&to=&project=&service=&type=&severity=&limit=
GET  /api/timeline?from=&to=&project=&bucket=  -- merged, bucketed snapshots + events
GET  /api/search?q=                        -- projects, services, env keys, files, events
GET  /api/overview                         -- every project with its current state
GET  /api/audit?before=&limit=             -- who changed Silt itself
SSE  heartbeat                             -- named, every 20s, so idle != wedged
GET  /api/stream                           -- SSE: snapshot.changed, event
POST /api/ingest                           -- generic external event webhook
GET  /healthz  /readyz  /metrics
```

`bucket` on `/api/timeline` is a duration (`1m`, `5m`, `1h`, `1d`); the server clamps it
so the response never exceeds ~2000 buckets regardless of what the client asks for.

### Diff response shape

```json
{
  "from": { "id": 811, "taken_at": 1740000000000 },
  "to":   { "id": 842, "taken_at": 1740003600000 },
  "summary": { "image": 1, "env": 2, "ports": 0, "volumes": 0 },
  "changes": [
    {
      "kind": "image_id",
      "service": "radarr",
      "path": "services.radarr.image_id",
      "op": "replace",
      "before": "sha256:aaaa…",
      "after": "sha256:bbbb…",
      "severity": "high"
    },
    {
      "kind": "env",
      "service": "radarr",
      "path": "services.radarr.environment.PUID",
      "op": "replace",
      "before": "989",
      "after": "1000",
      "severity": "medium"
    }
  ]
}
```

For a redacted key the `before`/`after` carry the HMAC placeholders, so the UI can say
"SECRET_KEY changed" without ever having had the value.

**Change kinds:** `image_ref`, `image_id`, `image_digest`, `env`, `ports`, `volumes`,
`networks`, `healthcheck`, `resources`, `command`, `entrypoint`, `restart_policy`,
`labels`, `depends_on`, `service_added`, `service_removed`, `state`, `other`.

**Severity heuristic:** `image_id`, `image_digest`, `volumes`, `service_removed` → high;
`env`, `ports`, `networks`, `command`, `entrypoint`, `healthcheck`, `depends_on`,
`service_added` → medium; `labels`, `resources`, `image_ref`, `restart_policy`,
`state` → low. Gaining `privileged` is high regardless of its kind: it is a
security-relevant change, not a footnote.

**Set fields compare as sets, ordered fields as wholes.** `ports`, `networks`,
`depends_on` and the capability lists carry no meaning in their order, so a
reorder is not a change — which is what normalisation bought and the diff must
not undo. `command`, `entrypoint` and `healthcheck` are order-sensitive and
compare as a single joined value.

**Mounts key on their container-side target**, not on the whole mount as an
opaque set member. A bind whose host source changed is one fact — "the /config
mount moved" — and set semantics would report it as an unrelated removal plus
addition, losing the before/after pairing that makes it legible.

### Ingest webhook

Accept a loose JSON body so Uptime Kuma's webhook notification, a `curl` from a cron job,
and a Home Assistant automation all work without a custom integration:

```json
{ "type": "monitor.down", "service": "radarr", "severity": "error",
  "message": "Radarr is down", "ts": 1740003600000 }
```

Everything except `type` is optional; `ts` defaults to receipt time. Match
`service`/`project` to a known project by name when possible; store unmatched events at
host level so they still land on the timeline.

Guard with `SILT_INGEST_TOKEN`, accepted **either** as `Authorization: Bearer <token>`
**or** as `?token=` — not every webhook source can set custom headers, and a webhook
nobody can call is not a feature. Compare in constant time (`subtle.ConstantTimeCompare`),
cap the body at 64 KiB, and rate-limit per source IP. If `SILT_INGEST_TOKEN` is empty the
endpoint returns 503, not 200 — fail closed.

---

## 9. UI

Six screens. Keep it boring and fast.

1. **Timeline** (home) — horizontal density strip (uPlot) over a filterable event/change
   feed. Filters: host, project, service, change kind, severity, time range. Live via SSE.
   Change markers and health events share one axis — this is the whole point of the app.
2. **Project** — snapshot list with change markers, current service table (image, digest
   short form, state, health, restarts, uptime), "compare last two changes" button.
   Show `compose_source` honestly: a badge when the compose portion is `unavailable`,
   and a warning when the on-disk files have drifted from what's running.
3. **Diff** — pick any two snapshots. Grouped by service, then by change kind, severity-
   coloured. Toggle between structured view and rendered-YAML side-by-side.
4. **Service** — image identity history (when did this image actually change, and to
   what), restart sparkline, env key change history from `env_keys`.
5. **Settings** — retention, notification targets and filters, ingest token, keep-list
   keys, manual prune/GC buttons.
6. **Search** — one box, reachable from anywhere with `/`. Projects, services,
   environment variable *names*, compose file paths and event text. Never values.

The **Projects** screen is the fleet view rather than a directory: what is running, what
is unhealthy, what has been restarting, what was edited but never applied, with every
count above the grid acting as a filter and the broken stacks first by default.

Container state uses one vocabulary everywhere — `web/src/lib/servicestate.ts`. Running,
starting, unhealthy, restarting, crashed, OOM-killed, stopped and paused each have a
colour and a word, and no screen invents its own. A container someone stopped on purpose
is grey and is not a fault.

Dark mode by default, light mode available. Every timestamp shows relative ("3h ago") with
the absolute UTC/local value on hover.

---

## 10. Repo layout

```
silt/
├── cmd/
│   ├── silt/main.go
│   └── silt-demo/main.go       # seeds a demo database; never shipped
├── internal/
│   ├── config/                 # env parsing, defaults, validation
│   ├── docker/                 # engine client, event stream, inspect normalisation
│   ├── compose/                # model building, normalise, redact
│   ├── collect/                # discovery, triggers, debounce, snapshot builder
│   ├── store/
│   │   ├── migrations/         # goose, embedded
│   │   ├── queries/            # .sql for sqlc
│   │   ├── blobs.go
│   │   └── retention.go
│   ├── diff/                   # structural diff + classification + severity
│   ├── api/                    # handlers, SSE hub, ingest
│   ├── notify/                 # shoutrrr wrapper + filtering
│   ├── demo/                   # the demo host, shared by `make demo` and e2e
│   └── web/
│       ├── web.go              # embed.FS
│       └── dist/.gitkeep       # committed — see Section 12
├── api/openapi.yaml            # hand-maintained contract
├── web/                        # Vite + Svelte 5 + TS
│   ├── src/
│   └── package.json
├── e2e/                        # Playwright; its own package.json so
│   ├── tests/                  # `npm --prefix web ci` stays fast
│   └── playwright.config.ts
├── Dockerfile
├── docker-compose.yml          # example: silt + docker-socket-proxy
├── .github/workflows/
│   ├── ci.yml                  # build, vet, test, npm build
│   └── release.yml             # buildx multi-arch → GHCR
├── sqlc.yaml
├── Makefile
├── LICENSE                     # AGPL-3.0
├── PROJECT.md                  # this document
└── README.md
```

---

## 11. Milestones

**M0 — Skeleton.** Go module `github.com/unmaykr-a/silt`. Config from env. slog. stdlib
HTTP server with `/healthz`. Vite+Svelte app that renders "Silt" and is served from the Go
binary via `embed.FS` — with `internal/web/dist/.gitkeep` committed and `//go:embed
all:dist`, so `go build` works on a clean checkout before npm has ever run. Multi-stage
Dockerfile that builds `linux/amd64` and `linux/arm64` per Section 12. Makefile with
`check`. CI green.
*Done when:* `docker run ghcr.io/unmaykr-a/silt` serves a page on both arches, **and**
`git clone && go build ./...` succeeds with no npm step.

**M1 — Docker collection.** Connect to the socket proxy. Discover projects from labels.
Subscribe to the event stream with debounce, the `exec_*` filter, and the full reconnect
contract from Section 5. Write events to stdout. No DB yet.
*Done when:* `docker compose up` on any stack prints a coalesced project-level change, and
restarting the socket proxy mid-run produces a reconnect plus a reconcile rather than
silence.

**M2 — Storage.** Migrations, sqlc, blobs with zstd + dedupe, snapshots, service_states,
env_keys, events, retention + GC. Container-derived model building, normalisation, and
**redaction** with the sentinel test from Section 7.
*Done when:* snapshots persist; restarting a container creates a snapshot with
`runtime_changed=1` and `config_changed=0`; changing an image tag produces
`config_changed=1`; an idle hour of interval snapshots adds under ~50 KB for 40 services;
the sentinel test passes.

**M2.5 — Compose file capture.** `SILT_COMPOSE_ROOTS`, raw file capture with
line-preserving redaction, a line-level diff, manual redaction marking, and
`config.drift` events.
*Done when:* editing a compose file without running `up` produces a `config.drift`
event rather than a phantom config change, and the file diff shows which line moved.

*Delivered after M6, expanded from the original scope.* Rather than loading the files
through `compose-go` and merging the model, Silt captures the file **text**, redacted
line for line. That answers the question people actually ask — which line changed — which
a merged model cannot: it reports that an environment key moved, not where in the file it
lives or what sits around it. `compose-go` interpolation is not used at all, so the hazard
Section 5 warns about (a `${VAR:?}` aborting the load for every project on the host) does
not arise.

**M3 — Diff engine.** Structural diff over normalised project models, classification,
severity. Table-driven tests covering every change kind.
*Done when:* `diff.Compute(from, to)` returns correct hunks for a hand-built fixture pair,
as a Go test — the HTTP endpoint lands in M4.

**M4 — API + SSE.** All endpoints from Section 8. SSE hub. Ingest webhook with token auth.
`api/openapi.yaml` written; `openapi-typescript` wired into the frontend build; contract
test green.
*Done when:* `curl` covers every endpoint and SSE streams a live change.

**M5 — UI.** The five screens from Section 9. Tailwind + shadcn-svelte, uPlot for the
density strip, SSE live updates.
*Done when:* you can find a change from the timeline and read its diff in three clicks.

**M6 — Ship.** Notifications via shoutrrr with kind/severity filtering. Forward-auth header
support plus optional password fallback. README with a copy-pasteable compose file (Silt +
socket proxy) and three screenshots. AGPL-3.0. Renovate config.

*The GHCR release workflow landed early, during M1: the example compose file referenced
an image that had never been published, which made the file unusable on a real host. It
publishes multi-arch on pushes to `main` and on `v*` tags.*
*Done when:* someone who has never seen the repo can be running it in under two minutes.

---

## 12. Gotchas that will cost you a day each

**`CGO_ENABLED=0` is non-negotiable.** Use `modernc.org/sqlite`, never `mattn/go-sqlite3`.
The moment cgo is in the build, arm64 cross-compilation needs a C toolchain and the
Dockerfile below stops working.

**`//go:embed` fails to *compile* if the directory is missing.** `web/dist` is gitignored,
so a clean checkout has no `internal/web/dist`, and `go build ./...` fails before any Go
code runs — breaking `make check`, the CI Go job, and M0 itself. Commit
`internal/web/dist/.gitkeep` and use `//go:embed all:dist`. Verify it in CI with a Go-only
job that never runs npm.

**Never let buildx emulate.** A Node build under QEMU takes ten-plus minutes and sometimes
just dies. Pin both build stages to the native builder and cross-compile:

```dockerfile
# Frontend: built ONCE on the native builder — the output is arch-independent
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /w
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Backend: also native builder, Go cross-compiles to the target
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /w/dist ./internal/web/dist
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /silt ./cmd/silt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /silt /silt
EXPOSE 8375
ENTRYPOINT ["/silt"]
```

`$BUILDPLATFORM` on both build stages is the whole trick. Both architectures build in the
same couple of minutes; only the linked output differs. Final image lands around 20–25 MB
with the UI inside it.

**Healthcheck exec events will drown you.** Every `HEALTHCHECK` probe emits `exec_create`
and `exec_start`. Filter them, or 40 services generate ~230k junk events a day.

**`RepoDigests` is empty more often than you expect.** Locally-built images have none, and
the field can hold multiple entries across registries. Fingerprint on `ImageInspect.ID`;
treat the digest as provenance. Never index `RepoDigests[0]` blindly.

**`compose-go` interpolation runs against *your* environment, not the operator's.** And
`${VAR:?msg}` aborts the load. Downgrade interpolation errors to warnings or one stack
takes down collection for the whole host.

**SSE through a reverse proxy needs `proxy_buffering off;`** on the nginx/NPM location, or
events arrive in batches minutes late. Document it. Send a heartbeat comment every 20s so
idle connections don't get culled by intermediaries.

**Digest, not tag.** `linuxserver/radarr:latest` is a moving target; it is exactly the case
Silt exists to catch. Always resolve and store image identity, never trust the tag.

**Normalise before diffing.** See Section 5. This is the difference between a useful tool
and a noise generator.

**Redact after interpolation, and keep-list rather than redact-list.** See Section 7.

**SQLite:** WAL mode, `busy_timeout=5000`, `foreign_keys=ON`. Open **two** pools against
the same file — a write pool with `SetMaxOpenConns(1)` and a read pool — rather than
relying on a mutex you'll forget to take. Set pragmas in the DSN so every new connection
gets them; a pragma set once on one connection does not apply to the pool. Concurrent
writers on a Pi's SD card will bite.

**Debounce everything.** One `docker compose up` fires a dozen container events. Editors
write files in several syscalls. Without debouncing you'll take twenty snapshots of one
change.

**Store UTC milliseconds.** Convert in the browser. Timezone bugs in a timeline app are
miserable, and second granularity collides on `UNIQUE (project_id, taken_at)`.

---

## 13. Config reference

Every variable Silt reads, grouped the way the Settings screen groups them. A test
(`internal/config/documented_test.go`) reads the `env` tags off the struct and fails if
one of them is missing from this table or from `.env.example`, because a setting that is
read but undocumented works perfectly and so nothing else ever notices.

`SILT_PORT` is not in this table on purpose: it is a compose-level variable that
`docker-compose.yml` uses to pick the published host port, and the process never reads it.

**Process and storage**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_LISTEN_ADDR` | `:8375` | Address inside the container. Change `SILT_PORT` instead unless you mean this |
| `SILT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SILT_DB_PATH` | `/data/silt.db` | SQLite file |
| `SILT_DOCKER_HOST` | `tcp://docker-socket-proxy:2375` | Docker API endpoint. A read-only socket proxy, never the socket itself — see Section 3 |
| `SILT_HOST_NAME` | `local` | Label for this Docker host in the database |
| `SILT_BASE_URL` | *(empty)* | Public URL. Links notifications to the diff and derives the OIDC callback |

**Collection**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_SNAPSHOT_INTERVAL` | `5m` | Reconcile cadence — catches whatever the event stream missed |
| `SILT_COMPOSE_ROOTS` | *(empty)* | Comma-separated absolute paths, mounted read-only, under which compose files may be read. An allowlist, not a hint |
| `SILT_MAX_COMPOSE_FILE_BYTES` | `1048576` | Cap on a single captured file |
| `SILT_KEEP_KEYS` | *(empty)* | Extra env keys kept in cleartext, `*` glob. Adds to the built-in safe list; there is no redact-list |

**Retention**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_RETENTION_DAYS` | `365` | Snapshots with `config_changed = 1`. `0` keeps forever |
| `SILT_UNCHANGED_RETENTION_DAYS` | `7` | Snapshots with `config_changed = 0` |
| `SILT_EVENT_RETENTION_DAYS` | `90` | Events — far higher volume than snapshots |
| `SILT_AUDIT_RETENTION_DAYS` | `730` | The administrative trail. A row per action rather than per observation, so it stays tiny and its value is how far back it reaches |
| `SILT_RETENTION_INTERVAL` | `1h` | How often the retention pass runs |
| `SILT_VACUUM_INTERVAL` | `0` | `0` disables; e.g. `168h` for weekly |

**Notifications**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_NOTIFY_URLS` | *(empty)* | Comma-separated shoutrrr URLs |
| `SILT_NOTIFY_ON` | `image_id,image_digest,volumes,service_removed` | Change kinds that notify |
| `SILT_NOTIFY_MIN_SEVERITY` | `medium` | ANDed with `SILT_NOTIFY_ON`: a change must match a listed kind **and** meet this severity |

**Authentication**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_LOCAL_ACCOUNT` | `true` | The built-in account. Off for an install that authenticates only through a provider or a proxy |
| `SILT_PASSWORD_HASH` | *(empty)* | bcrypt. Claims the built-in account before startup and takes the password out of the UI's hands |
| `SILT_TRUST_PROXY_AUTH` | `false` | Trust an identity header from the reverse proxy |
| `SILT_AUTH_HEADER` | `X-Remote-User` | Forward-auth identity header name |
| `SILT_AUTH_GROUPS_HEADER` | `X-Remote-Groups` | Forward-auth group header. Only read when `SILT_ADMIN_GROUPS` is set |
| `SILT_ADMIN_GROUPS` | *(empty)* | Groups in the header that mean administrator. Unset ⇒ every forward-auth identity is an administrator |
| `SILT_TRUSTED_PROXIES` | *(empty)* | Addresses or CIDRs whose auth header is believed. Empty with forward auth on means any client can assert an identity |
| `SILT_SESSION_TTL` | `720h` | Session lifetime regardless of activity |
| `SILT_SESSION_IDLE_TTL` | `168h` | Ends an unused session early. `0` disables |
| `SILT_OIDC_ADMIN_TTL` | `12h` | How long a provider-granted administrator role survives without a fresh sign-in. The session keeps working read-only after it. OIDC only; `0` disables |
| `SILT_COOKIE_SECURE` | `auto` | `Secure` on the session cookie: `auto` infers it from the request, `always` never guesses, `never` is for plain HTTP on a trusted network |

**OpenID Connect**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_OIDC_ISSUER` | *(empty)* | Enables OIDC. Must match the discovery document exactly, trailing slash included |
| `SILT_OIDC_CLIENT_ID` | *(empty)* | Registered client |
| `SILT_OIDC_CLIENT_SECRET` | *(empty)* | Registered client secret |
| `SILT_OIDC_REDIRECT_URL` | *(empty)* | Only if it is not `$SILT_BASE_URL/api/auth/callback` |
| `SILT_OIDC_SCOPES` | `openid,profile,email` | `openid` is always included whether listed or not |
| `SILT_OIDC_USERNAME_CLAIM` | `preferred_username` | Differs between providers |
| `SILT_OIDC_GROUPS_CLAIM` | `groups` | Some providers use `roles` |
| `SILT_OIDC_ADMIN_GROUPS` | *(empty)* | Groups that mean administrator. Unset ⇒ everyone admitted is an administrator |
| `SILT_OIDC_ALLOWED_GROUPS` | *(empty)* | Restricts who may sign in. Empty admits anyone the provider authenticates |
| `SILT_OIDC_ALLOWED_USERS` | *(empty)* | Same, by username |

**Everything else**

| Env var | Default | Purpose |
|---|---|---|
| `SILT_INGEST_TOKEN` | *(empty)* | Required for `POST /api/ingest`; empty ⇒ endpoint returns 503 |
| `SILT_INGEST_RATE_PER_MINUTE` | `60` | Events accepted per minute from one source address. `0` disables |
| `SILT_METRICS_PUBLIC` | `false` | `/metrics` without a session. It names every project on the host |

---

## 14. Identity

- Module: `github.com/unmaykr-a/silt`
- Binary: `silt`
- Image: `ghcr.io/unmaykr-a/silt` (Docker Hub as a discovery mirror)
- License: **AGPL-3.0** — the gap Silt fills is currently a paywalled feature elsewhere;
  AGPL keeps it from becoming one again. Note the asymmetry: MIT → AGPL is a decision you
  can make unilaterally today, while AGPL → MIT later needs the consent of every
  contributor. This is the one choice in the brief that gets *harder* to reverse, so it
  is being made deliberately at zero-contributor time.
- Platforms: `linux/amd64`, `linux/arm64`. State the `arm/v7` exclusion in the README so
  nobody has to discover it.
- Tagline: *what settled on your stack, and when.*

### Launch checklist (after it actually works)

1. README with a copy-pasteable compose file and three screenshots above the fold —
   this converts more than anything else you'll do.
2. Submit to **selfh.st/apps** (the directory people browse now).
3. PR to **awesome-selfhosted**.
4. Post to **r/selfhosted** and the Lemmy selfhosted community.
5. Show HN last, once the first round of issues is closed.

---

### More than one person (planned, not built)

Silt is built for one operator, and says so: `local_account` has a `CHECK (id = 1)`
because a table of users would be a user system nobody asked for. That holds. What does
not hold in a larger environment is everything *around* identity, and the parts worth
building are these, in order:

1. **An activity trail** — *shipped in 0.9.0.* Who changed a setting, ran a prune, signed
   in, was refused. It is the first question anyone asks the moment a second person can
   sign in, and it cannot be answered retroactively, which is why it came first.
2. **Read-only vs administrator** — *shipped in 0.16.0.* Silt is already read-only against Docker; the split
   that matters is between reading the journal and changing Silt's own configuration.
   The natural key is an OIDC group — `SILT_OIDC_ADMIN_GROUPS` alongside the existing
   allowlist — with everyone else able to read every screen and change nothing but their
   own appearance preferences. No roles table: the provider already manages groups, and
   duplicating them here would be two sources of truth that agree until they do not.
3. **Per-project visibility.** The harder one, and the one to resist until somebody asks.
   It means an authorisation check on every read path rather than one at the door, and it
   changes what search may return — a search that reveals the *names* of projects you
   cannot open is a leak that looks like a feature.

None of this needs a user table. Identity keeps coming from the provider; Silt keeps
recording who it was at the time.

## 15. What changed from the first draft

Kept from the original, unchanged and deliberately so: the read-only-by-architecture rule,
the locked tech stack, content-addressed blob storage, normalise-before-diff, SSE over
WebSockets, uPlot over D3, `$BUILDPLATFORM` on both build stages, and the milestone shape.

Changed:

1. **Identity** — `andri1305` → `unmaykr-a` throughout, matching the actual repo. LICENSE
   replaced with AGPL-3.0 to match Section 14 (it was MIT).
2. **`//go:embed` placeholder** — added to Section 12 and M0. Without it `go build` fails
   on a clean checkout and M0 cannot be completed as originally written.
3. **HMAC instead of sha256 for env values** — `sha256(value)[:12]` plus exact length is a
   brute-force oracle for any low-entropy secret. Per-install HMAC key plus length buckets.
4. **Keep-list instead of redact-list** — the original heuristic failed open on any key
   name the regex didn't anticipate. `SILT_REDACT_KEYS` removed, `SILT_KEEP_KEYS` added.
5. **Split fingerprint** — `config_fingerprint` / `runtime_fingerprint`. A restarting
   container no longer earns 365-day retention and a notification.
6. **Containers are the primary config source, files are enrichment** — the original had
   this reversed. Files can't be interpolated correctly from Silt's environment, can abort
   the load entirely on `${VAR:?}`, and may not reflect what's running. Added
   `compose_source` and `config.drift` events, and a new M2.5 to hold the file path.
7. **`image_id` alongside `image_digest`** — `RepoDigests` is empty for built images and
   ambiguous for multi-registry tags. Fingerprint on the local image ID.
8. **Event stream reconnect contract** — backoff, `since=` replay, full reconcile on
   reconnect, visible gap events. Previously unspecified.
9. **`exec_*` event filter** — healthcheck probes emit two events each; unfiltered that's
   ~230k junk rows a day for 40 services.
10. **Milliseconds, not seconds** — avoids `UNIQUE (project_id, taken_at)` collisions
    between event and interval triggers, and matches JS `Date`.
11. **Separate `SILT_EVENT_RETENTION_DAYS`** — events outvolume snapshots by orders of
    magnitude and shouldn't inherit the 365-day tier.
12. **Retention floor** — never prune a project's oldest surviving snapshot; it's the base
    for the earliest diff.
13. **Blob GC walks `inspect_hash` too** — the original only mentioned `compose_hash`,
    which would have deleted every inspect blob.
14. **Two new indexes** — `idx_env_keys_key` and `idx_service_states_service`, for the
    Service screen and the "when did this key change?" query.
15. **Socket proxy needs `VERSION=1` and `PING=1`** — the Docker client negotiates before
    it does anything; without them every call 403s.
16. **OpenAPI spec is hand-maintained** — there is no generator for stdlib `net/http`.
    Added a contract test so drift fails CI.
17. **Ingest hardening** — token via header *or* query (not every webhook source can set
    headers), constant-time compare, 64 KiB body cap, rate limit, fail closed when unset.
18. **SQLite two-pool setup** — one write connection, one read pool, pragmas in the DSN.
19. **Measured during M2** — an idle hour of snapshots cost ~670 KB rather than
    the budgeted 50 KB, because `service_states` and `env_keys` were written per
    snapshot and blob dedupe cannot touch relational rows. Observations that
    change nothing now touch the previous snapshot instead of inserting, and
    `env_keys` are content-addressed by inspect blob. An idle hour now costs
    zero bytes. `config_changed = 0` correspondingly now means "runtime-only
    change" rather than "nothing happened".
20. **Bind mount sources redacted** — the sentinel test caught a secret planted
    in a bind path flowing through verbatim, because the command-line redactor
    only handles `KEY=VALUE` shapes. Mounts are now structured so the host
    source can be redacted while type, target and mode survive.
21. **`taken_at` made monotonic per project** — millisecond timestamps still
    collide when two triggers land in the same millisecond, and
    `UNIQUE (project_id, taken_at)` turned that into a hard failure that lost
    the observation. Writes now advance past the previous snapshot's timestamp.
22. **Go floor 1.25 and sqlc's SQLite limits** — `docker/docker` v28 pulls in
    OpenTelemetry, which requires Go 1.25. sqlc could not infer types for
    `SUM`/`COALESCE` or disambiguate a self-referencing `DELETE` subquery on
    SQLite; both needed explicit `CAST`s and aliasing, as the brief's note about
    rough edges anticipated.
23. **Added during M4** — nothing wrote to the `events` table: M2 created it but
    only the collector's log recorded activity, so `/api/events` would have
    returned an empty list forever. Docker events, `snapshot.changed`, and
    ingested webhooks now all persist. `/metrics` is hand-written Prometheus
    exposition rather than pulling in the client library, since the brief
    defers anything beyond a basic endpoint and a handful of gauges do not
    justify a registry. The OpenAPI contract test checks both directions:
    every documented operation is reachable and shaped as declared, and every
    registered route is documented — verified by confirming it fails when a
    route is added without a spec entry.
24. **Added during M5** — Section 9's screens need data no endpoint in Section 8
    returns, so the API gained `/api/projects/{id}/services`,
    `/api/projects/{id}/services/{service}`, `/api/settings` and
    `/api/maintenance/prune`; assembling a service's history client-side would
    otherwise be one request per snapshot. The settings screen is read-only:
    Silt is configured by environment variables, and a screen that wrote to a
    database Silt does not read from would be a lie. The timeline zero-fills
    its buckets — a sparse series left uPlot to infer its own x-range, which
    produced an axis spanning years for a one-day window.
25. **shadcn-svelte installed by hand** — its CLI expects a TTY and hung
    indefinitely in a non-interactive environment. The components were fetched
    from the same registry the CLI reads, with its alias placeholders
    substituted. They are the real components, not a reimplementation. Only
    the ones actually used are kept, which is the model shadcn is built
    around. Note that its interactive primitives depend on `bits-ui` and its
    variants on `tailwind-merge`, so "no runtime dep" in Section 6 is not
    quite right: those add roughly 80 KB to the bundle.
26. **Added during M6** — notifications compare against the previous *changed*
    snapshot rather than the previous snapshot: runtime-only rows sit in
    between, and diffing against one of those would re-announce the same
    configuration change. The first change for a project is not announced,
    since "everything is new" is noise. Session cookies are signed with a key
    generated at startup, so a restart logs everyone out — acceptable for a
    single-user tool, and it means no long-lived secret to store. `Secure` is
    not set on the cookie because Silt is commonly reached over plain HTTP on
    a LAN, where a cookie that never arrives is worse than one relying on the
    operator's own network boundary.
27. **Compose file capture (post-M6)** — the files themselves are captured, not
    just the model derived from containers, so a diff can show which line
    changed. Three decisions carry it:

    **Line-preserving redaction.** A compose file can hold a literal secret and
    a `.env` file is nothing but secrets, so storing them verbatim would break
    the project's one promise. Values are replaced with keyed digests while
    every line, comment, indent, image tag and port stays exactly as written.
    A changed secret is therefore a visibly changed line without the value ever
    being stored. `${VAR}` references survive, since seeing which variable a
    service reads is itself worth noticing.

    **Compose roots are an allowlist, not a hint.** The paths come from
    container labels, and anyone who can start a container sets those. Silt
    resolves symlinks before deciding, so a link inside a mounted root cannot
    reach outside it.

    **A third fingerprint.** Files change independently of what is running, so
    an edited-but-unapplied file is `config.drift`, not a configuration change.
    Conflating them would report changes that never happened.

28. **Manual redaction marking (post-M6)** — the keep-list is a guess, and the
    person running Silt knows better. Marking works in both directions and
    beats the keep-list either way. It operates on a live preview that stores
    nothing, because a stored capture is already redacted and would leave
    nothing to decide about; the preview applies exactly what a capture would,
    so what someone marks against is what gets written. Hiding takes effect
    before anything is written, so a hidden value is never stored rather than
    stored and later concealed. Revealing applies only to future captures:
    earlier snapshots hold a digest, not the value.

29. **UI rebuilt for real scale (post-M6)** — the first design was tested
    against two projects and fell over at thirty. Inline project links wrapped
    over six lines and pushed content off screen, so navigation became a
    filterable sidebar. Every configuration change appeared twice, because the
    `snapshot.changed` event restated a change marker already rendered. A first
    boot produced a wall of identical rows, now collapsed into one. Container
    lifecycle chatter moved behind a toggle. The layout went full-width with
    the overflow contained rather than scrolling the page sideways.

30. **Settings are editable, as overrides on top of the environment (post-M6)**
    — the original brief said the settings screen is read-only, because "a
    settings screen that wrote to a database Silt does not read from would be a
    lie". That reasoning was right about the failure mode and wrong about the
    fix: the answer is to make the process read from it. The environment stays
    the baseline, `internal/settings` holds a sparse override document on top,
    and everything that used to cache a configuration value at startup now
    re-reads it — the collector's ticker, the retention pass, the redactor's
    keep-list, the notification sender, the ingest token, the log level. The
    whole merged document is validated before anything is written, so a
    rejected edit leaves the database and the running process exactly as they
    were, and a write that fails never becomes effective.

    What stays environment-only is not an oversight: the listen address and
    database path cannot change without a restart, and authentication, the
    Docker endpoint and `SILT_COMPOSE_ROOTS` are the boundary protecting the
    screen itself. A UI that could widen which files Silt reads, or turn off
    the login in front of it, would be a way in rather than a setting.

    Secrets keep the same promise they always had. The ingest token is
    write-only and reported as set-or-not. Notification targets are masked to
    their scheme, and their host only where that host is a server the operator
    chose rather than an identifier: a shoutrrr URL is the credential for the
    service it points at, and handing the list back would turn "can read the
    UI" into "can read the secrets".

31. **A changelog that is data, not prose (post-M6)** — the release history
    lives in `internal/changelog` as Go values, and `CHANGELOG.md` is generated
    from it with a test that fails when the two drift. The UI wants it
    structured — a release, a date, entries grouped by kind — and parsing prose
    back into that is a guessing game. `/api/version` reports the build stamp
    (a tag on a release, a commit otherwise) alongside the release number,
    because `sha-b0681bd` tells nobody which version they are on.

32. **The density strip became an instrument (post-M6)** — it drew the right
    picture and answered no questions: no hover readout, no way to act on a
    spike. Now a bucket names its counts on hover, a drag selects a window that
    the feed below follows, and a double-click or the range buttons take it
    back. uPlot's own zoom is off (`setScale: false`): the window belongs to
    the page, not to the chart, because the list underneath has to move with
    it.

33. **Navigation is a header, not a list (post-M6)** — the previous fix made
    the project list scale but left Settings at the bottom of it, which on a
    thirty-project host means scrolling the whole sidebar to reach it. Timeline,
    Projects and Settings are now top-level destinations in the header; the
    sidebar is projects and nothing else; and a Projects page lists every stack
    at once for when the sidebar is a scroll rather than a glance.

34. **Compose files are highlighted and diffed in the browser (post-M6)** —
    two things the server was never asked for, because neither is a fact Silt
    records. `internal/web`'s `yaml.ts` is a hand-written tokenizer: it
    highlights, it does not parse, and nothing downstream depends on it being
    correct YAML, so a highlighting library and its grammar is a large
    dependency for one panel. `linediff.ts` is the same LCS the Go side uses,
    plus a word-level pass over the lines that changed together — which is
    what turns "this whole line is different" into "this digest is different".

    Both are pure functions with unit tests, which is why `npm run build` now
    runs them. The tests earned their place immediately: they found `${VAR}`
    being split into four tokens, a `#` inside a URL being read as a comment
    start, and a "whole file" context that walked to the maximum safe integer
    one step at a time and locked the tab.

35. **Display preferences are per-browser, not per-install (post-M6)** — a
    24-hour clock and a dd/mm/yyyy date are properties of whoever is reading
    the screen, not of the Docker host. They live in localStorage beside the
    theme, so two people looking at the same Silt each get their own and
    neither can change the other's. The same store carries the navigation
    layout: sections across the top, or stacked in a left rail. Neither is
    right for everyone, which is what makes it a setting rather than a
    decision.

36. **The shell is a fixed-height column (post-M6)** — the rail and the content
    scroll separately. Before this the taller of the two decided the page
    height, so a forty-project list meant scrolling the whole document to get
    past it, and the timeline's own scroll position was hostage to how many
    stacks the host happened to run.

37. **Sessions are rows, not signed cookies (auth batch)** — the original
    scheme signed an expiry with a key generated at startup, which meant every
    restart logged everyone out and "sign out" only asked the browser to forget
    a token that stayed valid until it expired. An opaque random token recorded
    in `sessions` fixes both, and adds the thing an identity provider needs:
    something to attribute. Only the token's SHA-256 is stored, so a copy of the
    database is not a set of working sessions, and there is no signing key to
    protect because the token carries no claims to forge.

38. **OpenID Connect, with PKCE (auth batch)** — authorization code flow
    against any provider, discovered once at startup so a typo'd issuer
    surfaces before anyone tries to sign in. PKCE is used even though this is a
    confidential client with a secret: it costs one hash and closes the case
    where the code leaks through a proxy log or a Referer header before the
    exchange happens. The id_token's signature and its nonce are both checked —
    the signature says the provider issued it, the nonce says it belongs to
    *this* login rather than one replayed from elsewhere.

    `go-oidc` and `oauth2` are the two dependencies this batch adds. Hand-rolled
    JWKS fetching and RSA signature verification is precisely the category of
    code that should not be hand-rolled, so the rule that every dependency earns
    its place is what argues *for* them here.

    Group and user allowlists are optional and empty by default. The point of
    pointing Silt at a provider is to let the provider decide who gets in.

39. **Forward auth needs a trusted-proxy list (auth batch)** — the header was
    previously believed from any source, and anyone who can open a socket can
    set a header, so "authenticated" meant "reached the port". Silt's own
    documented deployment is a container on a bridge network with the proxy
    beside it: exactly the case where the port is reachable by every other
    container on that network. The trust list is checked against `RemoteAddr`
    and nothing else — reading `X-Forwarded-For` to decide whether to believe a
    header would be circular. An empty list still trusts any source, because
    some deployments genuinely have nothing else on the network, but it is
    warned about at startup rather than being the silent default.

40. **/metrics is authenticated by default (auth batch)** — it names every
    project on the host and counts their changes. It was on the public list
    because a Prometheus scrape is easier without a token, which is a reason to
    make the exception available, not to make it the default.
    `SILT_METRICS_PUBLIC` restores it.

41. **The rest of the security pass (auth batch)** — a cross-origin check on
    unsafe methods, so a page elsewhere cannot drive Silt through a signed-in
    browser; a content security policy that allows scripts only from Silt
    itself; per-client backoff on failed passwords, after a few free attempts
    so a typo costs nothing; the session cookie marked `Secure` when the
    request actually arrived over TLS rather than never; and the post-login
    destination reduced to a same-origin path, because an open redirect on a
    login endpoint is how a phishing link comes to look like it came from the
    real site.

42. **A fresh install is closed, not open (first-run batch)** — the previous
    default meant the safe configuration was the one you had to know to ask
    for, and the unsafe one was what you got by following the quick start. The
    built-in administrator now exists from first boot with no password, and
    that state locks the door: every request is refused, and the UI serves only
    a form asking for one. An unclaimed account that left the API open would
    make the setup screen decoration.

    The first-run window is real and is narrowed rather than claimed to be
    closed: nothing else is reachable while it is open, it is logged loudly at
    startup, and `SILT_PASSWORD_HASH` removes it entirely by claiming the
    account before the process starts. When it is set, the UI reports the
    password as the environment's and refuses to change it — declarative
    configuration should not silently diverge from what the screen says.

    One row, enforced by a CHECK. A table of users would be a user system
    nobody asked for; the way to have more than one identity is to point Silt
    at a provider that already manages them. Linking the account to a provider
    subject is what makes that a migration rather than a switch: sign in there,
    reach the same account, then turn the password off.

43. **The compose file is short enough to read (first-run batch)** — everything
    optional moved to `.env.example`, which documents every setting and says
    which ones need a restart. `env_file` is declared `required: false`, so a
    missing `.env` is not an error: Silt's defaults are a working install, and
    copying the example is what you do when you want to change one.

44. **Full file first (first-run batch)** — opening a project's compose files
    showed the diff. That is the wrong guess: the timeline already answers what
    changed and links straight to it, so someone who navigated to the files is
    almost always there to read the file.

45. **The issuer is passed verbatim (0.5.1)** — go-oidc compares the issuer in
    the discovery document against the string it was given, character for
    character. Silt was trimming the trailing slash first, and authentik both
    publishes and prints its issuer with one. The result was a provider that
    simply did not appear on the login screen, with the reason only in the log.

    Two lessons, both applied: normalising someone else's identifier is a
    guess, and a configured feature that silently does not appear is worse than
    one that says why. The provider's error now reaches the login screen.

46. **Setup is not always the first thing (0.5.1)** — an unclaimed built-in
    account locks the door only when it is the only way in. With a provider
    configured, that provider is the way in, and being forced to invent a local
    password first is friction with no security behind it. The setup form moves
    to the settings screen in that case, and the endpoint requires a session:
    once something else could admit someone, an anonymous claim would be taking
    an account that bypasses it rather than bootstrapping the only one.

47. **Search matches keys, never values (0.6.0)** — the obvious next feature on
    a host with forty-odd projects is a search box, and the obvious mistake is
    to let it reach environment values. Silt goes to real trouble not to store
    a secret in cleartext; a search that matched the ones it *did* keep would
    turn a UI convenience into a way to confirm a guessed value one query at a
    time. So `env_keys.value` is not searched at all, and there is a test that
    fails if it ever is.

    Two smaller decisions came with it. Wildcards are literal: `rada_r` does
    not match `radarr`, because a search box that quietly reinterprets what you
    typed is worse than one that finds nothing. And the term is matched with
    `instr(lower(col), ?)` rather than `LIKE`, which sidesteps escaping
    entirely — there is no metacharacter to escape.

48. **`internal/store/search.go` is hand-written (0.6.0)** — the third time
    sqlc's SQLite grammar has decided a batch. It rejects `ESCAPE`, it cannot
    parse `sqlc.arg()` inside `lower()`, and — the one that cost real time —
    given `LIMIT ?` after a construct it did not understand, it emitted `LIM`
    into the generated Go instead of failing. That compiles. It fails at
    runtime, where the handler logged the error and returned an empty result,
    which looks exactly like "nothing matched".

    The rule the repo now follows: sqlc for the queries it can generate, hand-
    written SQL in the same package for the ones it cannot, with a comment
    saying which limit was hit. Silent truncation is worse than a build error,
    so a generated query that looks wrong gets read, not trusted.

49. **The service page is a history, not a log (0.6.0)** — it listed
    observations, which is what the database has rather than what anyone came
    to find out. The question is almost always "when did this image change, and
    what changed with it". So: current state first, then one row per *image*
    with how long it held, and a link from each to the diff that introduced it.
    Finding that diff by hand meant going back to the project and matching
    timestamps.

50. **A digest and an image ID are different claims (0.6.0)** — a locally built
    image has no registry digest, and the page was falling back to the image ID
    under a "Digest" label. They are not interchangeable: one identifies the
    image on any host, the other only on this one. The label now names which of
    the two is on screen.

51. **An export that claims to be a diff has to be one (0.6.0)** — the first
    version emitted every line with a `-`/`+`/space prefix and no `@@` headers.
    It read fine and `patch` refused it, which is the worst combination: a file
    named `.diff` that no tool accepts. It now emits real hunks with three lines
    of context, and the check is not that it looks right — it is that applying
    it to the older snapshot's YAML reproduces the newer one byte for byte.

52. **Exports are ISO 8601, not the viewer's format (0.6.0)** — a diff copied as
    Markdown or downloaded as a unified diff leaves the browser that rendered
    it. dd/mm/yyyy is right on screen, where the reader chose it, and ambiguous
    in a file pasted into someone else's issue tracker. The export module
    therefore has no dependency on the preferences at all, which also keeps it
    testable: preferences live in a `.svelte.ts` rune module that the
    plugin-free vitest config cannot compile.

53. **Drift is a state, not an event (0.7.0)** — Silt has recorded
    `config.drift` since M2.5: a compose file changed and the running stack did
    not. As an event it answers "did this happen" and then scrolls away, while
    the file stays un-applied for weeks.

    The first attempt at a durable version read the latest snapshot's own
    `files_changed` flag, which is wrong in a way that only shows up later: the
    next unrelated container restart writes a snapshot with `files_changed=0`
    and the warning silently disappears. The predicate that holds is a
    comparison of two files fingerprints — the current one against that of the
    last snapshot where the running configuration actually changed. There is a
    test that fails against the flag-reading version with exactly that symptom.

54. **The Projects screen was a directory (0.7.0)** — a card per stack with its
    name and when it was last seen. On a host running forty-seven of them that
    is forty-seven cards all saying "2m ago", which answers nothing anyone came
    to ask. It now leads with state, and every count in the summary strip is a
    filter rather than a decoration: reading "3 unhealthy" and then hunting for
    which three was the specific thing it was worst at.

    `attention` is computed server-side. Had the browser decided it, the badge
    count and the row highlight would eventually disagree about what a problem
    is, and the one that is wrong would be the one someone trusts.

55. **Restart counts are not a rate (0.7.0)** — the tempting metric is
    "restarts in the last 24 hours", derived from the difference between two
    observations of `restart_count`. It goes negative exactly when a stack is
    redeployed, because `up` recreates the container and the counter starts
    again at zero. What is reported instead is the highest current count in the
    stack — the number `docker ps` shows — and the label says so.

56. **A notification target is wrong until something sends (0.7.0)** — a
    shoutrrr URL has no feedback loop. It is a string with a token in it, and
    the first thing that tries it is the change that mattered; Silt logs the
    failure, which helps whoever is reading logs at 03:10 and nobody else.
    There is now a button, and it reports each target separately rather than
    one verdict for the list, because the useful answer is *which* one.

    Two things fell out of building it. Errors are masked: providers quote the
    request URL back at you — gotify hands the app token straight back — so
    every fragment of the target is stripped from the message before it is
    rendered, since an error is a thing people paste into issues. And a test
    that plants a token in each of four provider URLs and checks it never
    appears in the output caught the gotify leak on the first run.

57. **A stopped container and an unhealthy one are not the same thing
    (0.8.0)** — reported from a real host, and true in three places at once.
    The service timeline painted unhealthy `bg-red-500` and exited
    `bg-red-500/70`: two shades of one red at the eight pixels a mark gets.
    The Projects screen folded exited, restarting, paused and created into a
    single "not running" count. The project's service table printed Docker's
    `state` and `health` in two columns and left "running / unhealthy" for the
    reader to interpret.

    They are different failures. An unhealthy container is *running*: the
    process is alive, the port is open, and the thing behind it is answering
    wrongly. A stopped container is not there. A restarting one is in a loop.
    Each now has its own colour and its own word, from one module
    (`web/src/lib/servicestate.ts`) that every screen reads, so a badge on the
    fleet view means what a dot on the service page means.

58. **A deliberate stop is not a fault (0.8.0)** — the corollary, and the part
    that changes behaviour rather than colour. `exited 0` is grey, is excluded
    from `Attention()`, and does not put its stack in the attention list.
    Treating every non-running container as a problem is how a dashboard
    teaches people to ignore it.

59. **Exit codes, and why OOM gets its own field (0.8.0)** — none of the above
    is possible without knowing *why* a container stopped, so
    `service_states` now carries `exit_code` and `oom_killed`.

    `exit_code` is NULL for a running container rather than 0. Docker reports
    the previous run's code while a container is up, and a stale 0 presented as
    current state reads as "exited cleanly" about something running fine —
    worse than showing nothing. `oom_killed` is a separate column because it is
    not derivable: an OOM kill and a `docker kill` are both 137, and only one
    of them is a memory limit to go and raise.

    The exit code is in the runtime fingerprint. Without it, a container that
    exited 0 and later exited 137 would be indistinguishable from the first
    stop and get touched onto the existing snapshot rather than recorded.

60. **Five controls in one corner is a row of widgets, not a header
    (0.9.0)** — search, a status dot, a version button, a theme toggle and a
    sign-out button, five shapes at one weight. Four of them are things you
    check occasionally and change more rarely, which is what a menu is for.
    Search stays outside because it is the one you reach for constantly and
    `/` has to land on something visible.

    Collapsing them was not only tidying: inside the menu each has room to say
    what it means. "offline" became "Not receiving updates — this page may be
    out of date"; the version gained its build stamp as selectable text,
    because that is the string that goes in a bug report.

61. **The live indicator lied (0.9.0)** — it went green on the first `ready`
    frame and stayed green through a server restart, a dropped network and a
    closed laptop lid. `subscribe()` only listened for *named* server events,
    and a connection dropping is not one: EventSource reports it through its
    own `error` event and reconnects by itself. Neither was observed.

    An indicator that cannot go backwards is worse than no indicator, because
    a stale page and a live one look identical and one of them is trusted.

62. **Reading state in a subscription callback re-entered the effect
    (0.9.0)** — the first fix for the above set status through a callback that
    compared against the current value. That read runs synchronously while the
    effect is being set up, so the effect took a dependency on the thing it
    was about to write: it re-ran, tore down the EventSource, opened another,
    and looped several times a second. The symptom was a status stuck on
    "Reconnecting…" over a server answering perfectly.

    The rule: comparison state a subscription callback touches stays outside
    the reactive graph. A plain `let`, not a rune.

63. **One clock, not one per timestamp (0.9.0)** — every `<Timestamp>` owned a
    30-second `setInterval`. Fine for a handful; the timeline renders a few
    hundred, each waking on its own phase, so the page updated as a ripple.
    One reference-counted ticker that stops when nothing is reading it.

    Its lifecycle lives in a plain `.ts` beside the rune module, because the
    vitest config has no Svelte plugin and cannot import a `.svelte.ts` — the
    same split `diffexport.ts` needed, now a pattern rather than a workaround.

64. **A local gate that does not match CI (0.9.0)** — `make check` skipped the
    gofmt step CI runs separately, so it passed locally and CI failed on a
    stray blank line. That is worse than having no local gate, because the
    local one is trusted. `make check` now depends on `make fmtcheck`, which
    runs CI's exact command.

65. **Silt journals itself (0.9.0)** — see Section 14. The trail records what
    changed and never what it changed to: the settings screen holds an ingest
    token and notification URLs, and this is a table built to be read. There
    is a test that plants sentinel secrets, changes those settings, and fails
    if either appears in the audit response — verified against a deliberately
    careless version that recorded the patch, which it caught.

    `actor` is a display string, not a foreign key. Identity comes from three
    unrelated places and none of them is a row Silt owns, so what is recorded
    is who it was at the time. An install with no authentication records no
    actor at all rather than inventing "admin", which would read as a real
    account on a Silt where anyone who can reach the port is one.

66. **"Already covered by the docker event" was not (0.10.0)** — the
    collector broadcast only on `ConfigChanged`, with a comment reasoning that
    runtime changes are covered by the Docker events that caused them. True
    when the UI showed configuration. False from 0.7.0, when the project
    screens began showing running counts, unhealthy and restarting.

    Wrong in two independent ways. **Ordering**: the Docker event is published
    the instant it arrives, but the snapshot it triggers is written after the
    two-second coalescing window — a browser refetching on that event reads
    the state from *before* the change, and nothing ever tells it to look
    again. **Coverage**: the interval sweep emits no Docker event at all, so a
    health flip it found was invisible until reload.

    The rule is now `shouldBroadcast(result)`, named and tested, because
    getting it wrong is invisible: the page simply shows yesterday and nobody
    notices until they reload. A touched snapshot stays silent — on an idle
    host of forty projects that would be one message per project per interval
    to say nothing happened.

67. **A keep-alive nobody can see (0.10.0)** — the SSE heartbeat was a comment
    frame. That does stop a proxy culling an idle connection, which was the
    job, but EventSource discards comments without telling anyone. So a live
    connection with nothing happening and one that had quietly wedged looked
    identical from the browser, which is the same blind spot that let the
    indicator lie in 0.9.0. It is a named event now, and the menu reports both
    "last heard from Silt" and "last change" — an idle host is silent about
    changes for hours and must still be able to prove it is being watched.

68. **A restart is news for a day (0.10.0)** — Docker's `restart_count` never
    resets, so a container that blipped once three months ago and has been up
    ever since kept its stack in the attention list forever. A list that is
    permanently non-empty is one people stop reading.

    The window needs no history query and no counter arithmetic. For a
    container with restarts, `started_at` *is* the moment of its last one — it
    has been up continuously since — so the age of that timestamp says whether
    the count still means anything. Containers that never restarted contribute
    nothing, or one long-running stack would make every restart look ancient.
    The raw count stays true and stays on screen in grey; the service page is
    where you go to ask about the past.

69. **One segmented control, and it slides (0.10.0)** — there were four
    hand-rolled ones, each moving its highlight by simply appearing somewhere
    else. A marker that jumps gives no sense of which way you went. It is one
    absolutely-positioned element measured from the selected button, because
    only one element can animate between two positions, and it is hidden until
    measured — sliding in from 0,0 on first paint reads as a glitch rather
    than as motion. `motion-reduce` turns it off.

70. **A keep key is a security boundary (0.11.0)** — found by asking what the
    settings endpoint accepts rather than what it rejects. `SILT_KEEP_KEYS`
    patterns were matched with `path.Match`, so `*` was a legal value that
    matched every environment variable on the host and stored all of them in
    cleartext. `**`, `?*` and `[A-Z]*` too. Nothing warned; the sentinel test
    passes because it never sets a keep key; and the failure is invisible from
    the UI, which shows the values as though keeping them were intended.

    The grammar is now exactly what the documentation always claimed: a name,
    optionally with a single `*` at one end. Validated at startup, validated on
    the settings screen, and — because this is the function that decides what
    is written in cleartext — an invalid pattern that reaches the matcher some
    other way is dropped rather than obeyed, so the failure mode is "your key
    was not kept" rather than "everything was".

71. **Accepting a value you cannot act on is not leniency (0.11.0)** — the
    same round found two more. `SILT_NOTIFY_ON=image` was accepted and matched
    nothing, so a typo meant "never notify" and the discovery was deferred to
    the outage. `SILT_BASE_URL=not a url` was accepted and became the link in
    a notification. Both now fail at the door, and the kind error lists the
    kinds. A setting that is wrong should say so while someone is looking at
    it, which is the same argument the notification test button was built on.

72. **The bug the sweep found was mine, and it was one I had already written
    down (0.11.0)** — the new sliding marker under the section links cleared
    itself with `{ ...marker, ready: false }`. That reads the marker, runs
    inside the effect that writes it, and so loops until Svelte aborts the
    page. It only fired where nothing is selected — `/search` and an unknown
    URL — which no screenshot of a working screen would ever have caught.

    Entry 62 is the same bug in the SSE status callback, written three
    releases earlier. Knowing the rule was not enough; what catches it is
    visiting the routes where the *empty* branch is the one that runs. The
    measurement is now a plain function in `web/src/lib/marker.ts` that never
    reads its previous value, with a test that fails if it starts to, and the
    sweep — every route at five widths in three configurations — is how this
    class gets checked from here.

73. **A one-off round is not a check (0.12.0)** — the previous release said
    the route sweep "is now how this gets checked". It was a script in a
    temporary directory, which is a thing that gets checked once. It is now
    `e2e/`: a real suite driving a real binary against a seeded database,
    running as its own CI job.

    Its own job rather than part of `make check` because it builds the
    frontend, seeds a database and drives a browser — a minute against a
    second — and the fast gate has to stay fast enough that people run it.

    What it asserts is deliberately shallow and wide: every route, at four
    widths, in three configurations, with no console error, no horizontal
    overflow, and something on the page. Depth belongs in unit tests; this
    catches the class they cannot see, which is a screen that only breaks in
    a configuration nobody screenshots.

74. **The seeder stopped being disposable (0.12.0)** — four batches in a row
    wrote a throwaway seeder, used it, and deleted it, each one slightly
    different and none of them covering drift. `internal/demo` is the shared
    one: fourteen projects covering every container state, an unapplied
    compose edit, and enough history for the graphs to have shape. `make demo`
    for development, the same data for the suite. It is not in the image —
    the Dockerfile builds `./cmd/silt` alone.

75. **Coverage where the bugs actually were (0.12.0)** — `internal/docker`
    sat at 47% and `normaliseInspect` had no tests, while being the function
    that reads every runtime fact the UI shows and carrying the newest logic
    in the project. Now 64%, with the distinctions that matter pinned: an
    exit code only from a stopped container, zero distinct from none, an
    absent healthcheck distinct from a healthy one.

    `internal/collect` remains the weak one at 22%, and honestly so: the bulk
    of it is the Docker-dependent pipeline, and the fake engine that would
    make it testable is a test-only type in another package. Its pure
    decisions — event severity, batch summaries, and the broadcast rule that
    shipped a bug — are covered. Promoting that fake engine to a shared
    testing package is the next real step, not a claim to have taken it.

77. **The fake engine became a package, and the pipeline got tests (0.13.0)**
    — entry 75 named promoting the fake Docker engine as the next real step
    and this is it. `internal/docker/dockertest` is one implementation with
    container and image inspection added; the Docker tests use it instead of
    their own copy, and `internal/collect` uses it to drive the whole
    pipeline — engine to store to broadcast — through a harness rather than
    around it. Coverage there went 22% → 35.4%, and the parts that moved are
    the parts that had shipped a bug: a runtime-only change reaching the
    browser is now a test rather than a fix nobody can regress against.

    The internal-test detail that made it cheap: `dockertest` imports nothing
    from `internal/docker`, so `package docker` can use it with no cycle. The
    first attempt moved `events_test.go` to `package docker_test` and
    cascaded into a dozen undefined identifiers for no gain.

78. **The demo is the app, not a copy of it (0.13.0)** — a published demo
    could have been a second implementation with its own data layer. It would
    have drifted within a release, and the first thing anyone would learn
    from it is how the demo behaves. Instead `web/src/lib/demo.ts` is a shim
    over `fetch`: the same components, the same API client, the same router,
    answering from responses captured off a real Silt reading the `make demo`
    database. Only the transport differs.

    Three things the shim has to supply that a capture cannot:

    - **Time.** A capture is one instant; a demo is read for months. Every
      timestamp is shifted onto the reader's clock at load, by an allowlist
      of field names rather than "any number that looks like an epoch" —
      `from` and `to` carry snapshot ids on `/api/diff`, and a silently
      shifted id would be a bug nobody would think to look for.
    - **Timeline windows.** Both range pickers ask for `from`/`to` derived
      from `Date.now()`, which no fixed key can match. The nearest captured
      range answers and its buckets slide onto the requested window, which
      keeps the histogram's shape.
    - **Error responses.** The demo mounts no compose roots, so a file
      preview genuinely is a 503. Captured and replayed, the screen shows its
      own explanation; dropped, it would show "no demo data", which is a lie
      about why the panel is empty.

    Writes are refused with a message rather than faked, and the connection
    indicator gets a fourth state: opening an EventSource against a file host
    is a permanent reconnect loop reported as "offline" — technically true
    and useless.

79. **Base paths, because Pages serves under one (0.13.0)** — every href in
    the app is written app-relative, which is identical under a base of "/"
    and wrong under `/silt/`. Intercepting the click would have been enough
    for a plain click and nothing else: the status bar, ctrl-click,
    middle-click and "open in new tab" would all have left the app. The
    `link` action rewrites the attribute instead, so there is one place that
    knows about the mount point. `parseRoute` is fed a base-stripped path,
    with a prefix match that is not merely a prefix match — `/siltation` is
    not under `/silt`.

    The published site's own trick is `404.html` as a copy of `index.html`:
    Pages serves it for unknown paths, which is what makes a deep link
    survive a reload with no server to fall back.

80. **The demo verifies itself (0.13.0)** — its failure mode is quiet. A URL
    the capture never reached answers 404 and the screen shows its own empty
    state, which published is a blank panel with nobody to notice.
    `make demo-site-verify` serves the built site the way Pages serves it and
    drives every screen in a browser, failing on the shim's own "no demo
    data". It walks the real routes rather than replaying the capture's URL
    list, because the two agreeing proves nothing: the question is whether
    the screens' requests are covered.

81. **A locale setting blanked the page (0.13.0)** — found by the demo's own
    verification, which happened to run in a container whose Chromium
    reported `en-US@posix`. That is what a browser says under `LANG=C`,
    `LANG=POSIX` or `LANG=en_US@posix`, and it is not a valid BCP 47 tag.
    uPlot builds `new Intl.NumberFormat(navigator.language)` at module scope,
    so importing the chart threw, so the bundle threw, so there was nothing on
    the page at all — no error state, no partial render, on a setting with
    nothing to do with charts.

    Two fixes, because there are two exposures. `web/src/lib/localeguard.ts`
    wraps `Intl.NumberFormat` and `Intl.DateTimeFormat` in a Proxy that retries
    a `RangeError` with a repaired locale, and is imported first in `main.ts`
    for that reason — sibling imports evaluate in source order, and the
    formatter is built inside a dependency at import time. The first attempt
    patched `navigator.language` instead and the test for it failed: a page
    can define that property non-configurably, and a repair that cannot be
    applied is no repair. `Intl` is a plain writable global, a Proxy keeps
    `instanceof` and the statics intact, and nothing happens at all unless a
    construction actually throws.

    `web/src/lib/locale.ts` covers our own formatting, which does not go
    through those constructors: the system locale is probed once and a fixed
    one stands in if it cannot be used, so a `toLocaleString` is a
    wrong-looking timestamp at worst rather than a blank screen.

    The lesson worth keeping is about the verification, not the locale: a
    check that drives the real thing in a real browser found a total failure
    that every unit test, every type and every screenshot of a working screen
    had passed over.

82. **The demo was demonstrating the wrong thing (0.14.0)** — the seed gave
    every project seven observations where one image tag gained the suffix
    `-alt`, so every diff in the published demo read
    `radarr:5.4.0 -> radarr:5.4.0-alt`. On the screen the project leads with,
    that is a screenshot of the feature not working.

    The seed now carries a `Change` list per stack, replayed forward and
    accumulating, so the diff between two adjacent snapshots is exactly the
    change that happened between them. One stack has a real sequence: a patch
    release alone, a port and a mount arriving together, then an upgrade
    landing with a rotated API key and a new setting — one snapshot with four
    changes across three severities, which is what a single `compose up`
    actually looks like and what the grouping is for.

    Two smaller lies went with it. Image IDs were derived from the service
    name, so they never changed and the image history had one row however many
    upgrades it contained. And the compose files were written into the
    observation directly rather than through `Redactor.ComposeText`, so the
    demo displayed a literal API key in cleartext — on the one screen whose
    entire point is that it never stores one.

83. **A verification that only ever opened the empty case (0.14.0)** — the
    capture derived per-file diff paths from `/api/diff`, which does not
    return files: the file diff belongs to the Files screen and comes from
    `/api/diff/file`. So the loop iterated an always-empty array and captured
    nothing, and the published demo's compare view had no data behind it.

    It passed `make demo-site-verify` because the check only visited
    `/diff?project=N`, which renders "pick two snapshots to compare" and asks
    for nothing. The check now opens a diff with two snapshots selected, and
    the capture derives its paths from the snapshots' own file lists.

    The general shape is worth keeping: a check that only exercises a screen's
    empty state verifies the empty state. Entry 80 said the demo verifies
    itself; it verified the half of it that needed no data.

84. **Light mode was the display's maximum (0.14.0)** — stock shadcn light is
    `oklch(1 0 0)` behind `oklch(0.141 …)`, on a screen people leave open all
    day, with a card the same pure white as the page behind it. Off the
    extremes at both ends: the page is an off-white, the card is the white and
    now reads as raised, and the foreground comes off black at around 12:1.
    Secondary text moved the other way — the stock `muted-foreground` on white
    was about 3.5:1, under AA, so a comfort change and an accessibility fix
    pointed in opposite directions and both were needed.

85. **The settings screen needed an index, not more sections (0.15.0)** —
    nine sections and forty-odd fields is past the point where "it is in here
    somewhere" works, and the answer is not fewer sections: every one of them
    earns its place. `web/src/lib/settingsindex.ts` is the screen's contents as
    data — every setting, the section it lives in, the environment variable
    behind it, and the words someone would actually type.

    The variable matters as much as the label. The compose file is where people
    know these by name, so the string in hand when the question comes up is
    `SILT_KEEP_KEYS`, not "keys kept readable" — and nothing on the screen could
    be searched by it.

    Substring rather than fuzzy, deliberately: a settings search that answers
    "keep" with "Vacuum" because the letters appear in order is worse than one
    that answers nothing. And the index is kept beside the screen rather than
    generated from it, because the screen is markup and a test that reads
    markup pins the markup. The one drift that matters — a field rendered with
    no entry here, invisible to search and noticed by nothing — is what
    `settingsindex.test.ts` checks, by reading the field names out of the
    component and asserting the index covers them.

86. **Twelve settings were readable nowhere (0.15.0)** — the whole OIDC client,
    the trusted-proxy list, the identity header, the session lifetimes. Not
    editable, correctly: they are the boundary protecting the settings screen,
    and a UI that could edit them would be a way in rather than a setting. But
    not *shown* either, which is a different decision that nobody made.

    When forward auth is not working the first question is what Silt thinks it
    was told, and the only way to answer it was to go and read the compose file
    on the host. The Authentication section answers it, with secrets reported
    as configured-or-not exactly as the notification targets and the ingest
    token already were.

87. **A configuration review, because unset is not the same as unmeant
    (0.15.0)** — Silt is thirty-odd environment variables, most of which do
    something sensible when unset. That is the right default and it hides a
    specific failure: a setting that is *almost* right produces no error at
    startup and no symptom until the day it matters.

    `config.Config.Checks` is the reading of the whole environment that an
    operator would otherwise have to do from memory. Forward auth trusted with
    no proxy list. Notifications configured with no base URL, so the one
    message that needed to be useful links nowhere. Runtime-only snapshots
    outliving the changes they sit between.

    Deliberately not validation — `Validate` refuses to start on anything
    wrong, and these are the things that are legal and probably not what was
    meant, which is why they are advice. Each names the variables involved,
    because a finding you cannot act on is a finding that gets ignored.

88. **Releases are the changelog, rendered twice (0.15.0)** — the repository
    published images from the first milestone and never published a release,
    so the README's release badge pointed at an empty page. The fix is not a
    release-notes file: `changelog.Notes(version)` renders one entry from the
    same data `CHANGELOG.md` is generated from, and the workflow fails the job
    when a tag names a release the changelog does not have — which is the shape
    of every "tagged the wrong thing" mistake. Notes written separately are
    notes that disagree with the changelog by the second release.

90. **One rule, not a list of protected routes (0.16.0)** — the reader/
    administrator split could have been a per-route allowlist. It is a single
    predicate instead: every safe method is readable by anyone signed in, and
    every unsafe one under `/api` that is not part of authenticating needs an
    administrator.

    A per-route list is a list that grows a hole the next time an endpoint is
    added, and the hole is silent — the endpoint simply works for everyone, and
    nothing fails to tell you. The tests are written against the same shape:
    they enumerate the write endpoints and assert 403, so an endpoint added
    without a test is still covered by the rule.

    The role is stored on the session rather than looked up per request. The
    answer comes from the provider's groups at sign-in, and asking again on
    every request would put an outage at the identity provider between a reader
    and a page they are allowed to read. The column defaults to `admin` so an
    existing session survives the upgrade: everyone who could sign in before
    could change everything, and silently demoting them would look like Silt
    breaking.

    The cost of storing it is that a demotion is not retroactive: removing
    someone from the administrator group takes effect at their next sign-in,
    up to the session lifetime away. Forward auth has no such lag, because the
    proxy asserts the groups on every request and the role is recomputed from
    them each time. That asymmetry is worth knowing rather than worth removing
    — the remedy for the sticky case already exists and is one button, "end
    every session", under Security — so the Authentication screen says so.

91. **Probes ask; checks read (0.16.0)** — the setup review added in 0.15.0
    reads the configuration and says what looks unintended. It cannot say
    whether the Docker endpoint answers or whether the compose root you
    configured is actually mounted, and that second failure is the nastiest one
    Silt has: a root that was never mounted renders exactly like a project with
    no files, on every screen.

    On demand rather than on the settings payload, because each probe touches
    the network or the filesystem and a settings screen that hits the Docker
    socket every render is one nobody should open during an incident. The
    elapsed time is reported alongside the result: an endpoint answering in
    four seconds is working, and worth knowing about.

92. **The export is the override document (0.16.0)** — settings are already
    stored as a sparse patch on top of the environment, so "export" is that
    document with a header rather than a new format, and there is no import
    endpoint at all: `PUT /api/settings` already takes this shape, and a second
    write path would be a second set of validation rules to keep in step.

    Secrets are stripped and *named*. A shoutrrr URL carries the credential for
    the service it points at and does not become readable by being called an
    export — but a file that silently omits your notification targets is a
    restore that silently stops notifying, so the file says which ones you will
    have to set again.

93. **Releases publish on merge, not on a tag (0.16.0)** — the tag-triggered
    workflow added in 0.15.0 was correct and never ran, because pushing the tag
    is a manual step and manual steps get forgotten. It had been forgotten for
    fifteen releases; the release badge in the README pointed at an empty page
    the whole time.

    The changelog already names the version, so the merge is the trigger: if no
    release exists for the version at the top of `internal/changelog`, CI
    creates the tag and publishes the notes. Idempotent, so every other push to
    main finds the release already there and stops. Tagging by hand still
    works, and `make release` still does it — it is just no longer the only
    path, and no longer the one that has to be remembered.

94. **Setup is open exactly once, and only while it is the only door (0.17.0)** —
    `POST /api/auth/setup` claims the built-in account, and it was guarded by
    "are you signed in" rather than "are you an administrator". On an install
    where a provider is the way in and the built-in account was never claimed,
    that made every viewer one request away from an administrator password.

    The rule is now the same one everything else uses: if there is already
    another way in, claiming the account is an administrative act and needs an
    administrator. If there is not — a fresh install, where the setup form is
    the only thing on screen — it stays anonymous, because there is nobody to
    ask and no account to escalate from. The two cases look identical in the
    handler and are not the same question, which is exactly why the first
    version got it wrong.

95. **The vacuum clock has to survive a restart (0.17.0)** — `lastVacuum`
    started at the zero value, which is an infinitely long time ago, so the
    first retention pass after every start vacuumed. A container that restarts
    nightly turned a weekly VACUUM into a nightly rewrite of the entire
    database file.

    Starting the clock at `time.Now()` instead has the opposite failure: a
    container restarted more often than the interval never vacuums at all.
    Neither in-memory answer is right, because the thing being measured is
    longer than the process's life. The time goes in the settings table, which
    is the only version where the configured cadence is the actual cadence.

    The same shape is worth watching for anywhere else a long interval meets a
    short-lived process.

96. **Resolve the roots, not just the path (0.17.0)** — the compose-root check
    resolves symlinks before deciding, which is what stops a link inside a
    mounted root from reading the rest of the filesystem. It compared the
    resolved path against the *unresolved* roots, so a root that was itself a
    symlink — `/srv` pointing at external storage, the ordinary shape on a
    small host — never matched anything under it.

    It failed closed, which is why it took this long to find: nothing leaked,
    the screen simply said no files, and that is indistinguishable from a stack
    with no compose file. A security check that fails closed and silently is
    still a bug, and a harder one to see than the loud kind.

97. **An API path answers as an API (0.17.0)** — `mux.Handle("/", web.Handler())`
    is the right catch-all for a single-page app and it also caught every
    mistyped API path and every wrong method, answering both with a document of
    HTML and a 200. A caller then has a success it cannot parse.

    `/api/` now has its own fallback, which asks the mux which methods the path
    does accept and answers 405 with them named, or 404 when there is no such
    path. It asks the mux rather than keeping a second list of routes, because
    a second list is a list that goes stale.

98. **Documentation drift is a test, not a habit (0.17.0)** — sixteen settings
    were readable only in the struct, the whole OpenID Connect block among
    them, which is exactly what someone is looking for when a login will not
    work. Nothing noticed, because a setting that is read and undocumented
    works perfectly.

    `internal/config/documented_test.go` reads the `env` tags by reflection and
    fails when one is missing from the reference table or from `.env.example`,
    and in the other direction when the example offers something nothing reads
    — `docker-compose.yml` counts as a reader there, which is how `SILT_PORT`
    stays legitimate.

99. **The rest of the pass (0.17.0)** — the collector listed every host and project per Docker
    event to find one project, now one indexed lookup; the settings export
    filename is sanitised rather than interpolated straight from the host name;
    the Files route is keyed on its path so a link to a different file
    remounts; two a11y build warnings fixed rather than suppressed, so the next
    real one is visible.

100. **A role nobody read (0.18.0)** — `SILT_OIDC_ADMIN_GROUPS` shipped as the
    headline of 0.16.0, was reported as in effect on the settings screen, and did
    nothing. The callback built its `auth.Identity` by hand and left `Role`
    unset; `ParseRole("")` defaults to admin, deliberately and for good reasons,
    so every account the provider admitted became an administrator.

    Everything around it was tested. `RoleFromGroups` had its own tests, the
    middleware had six, and `OIDC.Finish` — which does set the role — had a full
    fake provider behind it. Nothing tested the one line that mattered, because
    the handler had quietly stopped calling `Finish` when the account-linking
    flow needed `Exchange` instead, and taken the identity-building with it.

    The lesson is not "test more". It is that a decision with two callers wants
    one implementation: `auth.IdentityFor` is now the only way claims become an
    identity, it lives beside the rules it applies, and a source-level test
    fails if `internal/api` grows its own again. Duplicating a decision to reach
    a different half of it is how a security control becomes decorative.

101. **The link says which account, not whether (0.18.0)** — checked first and on
    its own, the account link skipped the sign-in allowlist entirely. Removing
    someone from the permitted group left them signing in as the administrator.

    Two questions that look like one: may this person in, and which account do
    they land in. The link is still the more specific statement — it should beat
    a group membership about *identity* — but it was never authority to be
    admitted. Ordering the checks is the whole fix.

102. **An answer read once needs an expiry (0.18.0)** — a provider's groups are
    read at sign-in and nothing re-reads them, because there is nothing to
    re-read them from without storing a refresh token. With a 720h session that
    made a demotion take up to a month.

    `SILT_OIDC_ADMIN_TTL` expires the administrator half rather than the session:
    after 12h the session keeps working, read-only, until the next sign-in.
    Shortening `SILT_SESSION_TTL` instead would have signed everyone out on a
    timer to fix a problem that is not about reading, and re-checking groups per
    request would put an outage at the identity provider between a reader and a
    page they are allowed to read.

    Scoped to OIDC admins on purpose. Forward auth re-reads its groups from the
    header every request and is never stale; the built-in account has no
    provider to have changed its mind, and expiring its rights would lock the
    sole operator out of their own settings screen on a timer.

103. **A token is not a rate limit (0.18.0)** — the ingest webhook checked the
    token, capped the body at 64 KiB, compared in constant time, and had no
    limit on how often. PROJECT.md said "rate-limit per source IP" and had said
    it since M4; nobody built it. A webhook token lives in an Uptime Kuma
    config, a cron script and a Home Assistant automation, so it is the
    credential most likely to be read by someone who should not have it, and
    one copy was unbounded writes into the timeline.

    Checked *after* the token, which is the part worth stating: a limit an
    unauthenticated caller could spend would be a way to silence someone's
    monitoring rather than a way to protect it.

104. **Inference that fails open is not a default (0.18.0)** — the `Secure` flag
    on the session cookie was inferred from the request, and the inference is
    right almost always. The exception is a reverse proxy that terminates TLS
    and does not set `X-Forwarded-Proto`, which is a configuration people arrive
    at by accident: Silt sees plain HTTP and sends the session cookie without
    `Secure`, over a connection the browser will happily repeat in the clear.

    `SILT_COOKIE_SECURE=always` is the way to stop it guessing, and an
    `https://` base URL now counts as having answered — someone who told Silt
    its public address is HTTPS should not have to say so twice.

105. **The history needs a way out (0.18.0)** — Silt's entire pitch is a record
    you cannot reconstruct, and there was no supported way to keep a copy of it.
    "Copy `silt.db`" is the obvious answer and it is wrong: the database runs in
    WAL mode, so the committed state is spread across three files, and a copy of
    the first one opens cleanly while missing whatever had not been
    checkpointed. The failure surfaces on the day you restore it.

    `VACUUM INTO` is the right primitive — a read transaction, so one consistent
    snapshot including the WAL, written as a single compacted file with no
    sidecars. It runs on the read pool, because putting it on the single writer
    would block every snapshot for the length of the copy.

    A download rather than a scheduled job writing somewhere: Silt does not know
    where your backups live, and a tool that writes files into paths it was given
    is a tool with a new class of bug. Administrator only — redaction means the
    values are digests rather than secrets, but "may read the screens" and "may
    walk off with the database" are not the same permission.

106. **A read that is not a screen (0.18.0)** — the write guard is one rule:
    every safe method is readable by anyone signed in. That is right for
    everything it guards, and the backup endpoint is the exception it could not
    see. A GET that hands over the whole database is not a page, and the guard
    waved it past — a viewer could download every project, every captured
    compose file, the audit trail and the session table.

    Found by writing the test that asserts otherwise, which is the argument for
    writing that test even when the answer looks obvious. The check is now
    explicit in the handler, with a comment saying why it is not left to the
    guard, because the next endpoint of this shape will want the same.

107. **One file per section (0.19.0)** — the settings screen reached 1,586 lines
    with ten sections inlined into it, so every change to Notifications was a
    change to the file Retention was in. Each section is its own component now
    and the screen is 270 lines of rail, search and save bar.

    Worth being honest about the accounting: the total went *up*, by about 340
    lines. Sixteen files cost sixteen sets of imports and prop declarations, and
    each one carries the comment explaining what it is for. What went down is
    the size of the thing you have to hold in your head to change one section,
    which was the actual complaint — "bloat" measured in total lines was never
    the problem, and optimising for it would have argued against the split.

    The shared state is a factory rather than module-level state, because
    module-level state outlives the screen: navigating away and back would show
    the previous visit's unsaved draft and stale error. It is passed to panels
    as a prop rather than through context, so a panel's dependencies are visible
    where it is used.

108. **Extract the part that can be tested (0.19.0)** — `buildPatch` decides
    which fields a save actually sends, and it had no tests, because it lived in
    a `.svelte` file and the test runner deliberately has no Svelte plugin.

    Being wrong there is silent in a specific way: a patch that restates a field
    nobody touched writes an override for it, and that field then stops tracking
    the environment it was set from. Nothing looks different until the next
    container recreate, when an environment change does not take and there is no
    obvious reason why.

    It is a plain `patch.ts` beside `store.svelte.ts` now — the same split
    already used for `clockcore.ts`, `marker.ts` and `settingsindex.ts`. The
    rule is worth stating as a rule: reactive glue in the rune module, the
    decision the glue is wrapping in a plain one.

109. **Three renderers for one idea (0.19.0)** — "a setting Silt reports but will
    not let you change" was written three times, in two sections, and had
    drifted: the hint sat under the value in Security and under the label in
    Authentication. Two lists of the same kind of thing did not look like it.

    The label's version won. A hint explains what the setting is; the value is
    the answer. Prose in the answer column makes the column you are scanning
    ragged, which is the whole reason to have a column.

110. **Smaller** — ASCII redaction placeholder instead of guillemets; `bucket` param on
    `/api/timeline` with a server-side clamp; `SILT_NOTIFY_MIN_SEVERITY` semantics
    specified as AND; M3's done-criterion is a Go test rather than an endpoint that
    doesn't exist until M4; fsnotify watches the parent directory so atomic saves don't
    deafen the watch; explicit inspect-field allowlist so the fingerprint split isn't
    defeated at source.
