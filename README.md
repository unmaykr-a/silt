<div align="center">

<img src="docs/icons/silt.svg" width="110" height="110" alt="Silt" />

<h1>Silt</h1>

<p>A self-hosted change journal for Docker Compose stacks.<br />
<em>What settled on your stack, and when.</em></p>

<p>
  <a href="https://github.com/unmaykr-a/silt/releases"><img alt="Release" src="https://img.shields.io/github/v/release/unmaykr-a/silt?style=flat&label=Release&color=34d399&labelColor=18181b" /></a>
  <a href="https://github.com/unmaykr-a/silt/pkgs/container/silt"><img alt="Image" src="https://img.shields.io/badge/ghcr.io-unmaykr--a%2Fsilt-34d399?style=flat&labelColor=18181b" /></a>
  <a href="LICENSE"><img alt="Licence" src="https://img.shields.io/badge/License-AGPL--3.0-34d399?style=flat&labelColor=18181b" /></a>
  <a href="https://github.com/unmaykr-a/silt/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/unmaykr-a/silt/ci.yml?style=flat&label=CI&color=34d399&labelColor=18181b" /></a>
  <a href="https://ko-fi.com/unmaykr"><img alt="Support" src="https://img.shields.io/badge/Support-Ko--fi-34d399?style=flat&labelColor=18181b" /></a>
</p>

<p><b><a href="https://unmaykr-a.github.io/silt/">Try the live demo →</a></b><br />
<sub>The whole UI, in your browser, against a made-up host. Nothing to install.</sub></p>

<br />

<img src="docs/screenshots/timeline.png" alt="Silt timeline" width="900" />

</div>

<br />

## Overview

Silt watches your Docker host and records, over time, the effective configuration of every
Compose project, the resolved image identity of every service, container state, and a
stream of events — then lets you answer one question well: **what changed, and when?**

When something breaks at 03:10, you can see the image that got pulled at 03:00.

Silt **never writes to the Docker API.** It observes, through a read-only socket proxy. That
is an architectural rule, not a v1 shortcut.

<br />

## What it does

- **One timeline.** Config changes and health events share a single axis, because a change
  and the outage it might explain are only useful side by side.
- **Diffs between any two snapshots**, grouped by service and coloured by severity.
  Structured or as YAML, with a real unified diff you can `git apply`.
- **Secrets that were never stored.** Environment values are redacted by default and kept as
  a truncated HMAC under a per-install key, so the history can tell you *when* a key changed
  while being useless to anyone holding the database file.
- **Your compose files, line by line** — captured on every change, redacted value by value,
  with every comment and indent left exactly as written. Watched, so an edit is on the
  timeline within a second of the save.
- **Unapplied edits.** Editing a file without running `up` is a state Silt reports, not just
  a moment it logs.
- **External events.** An Uptime Kuma probe or a cron job can post to the timeline, so a
  failure sits beside the change that caused it.
- **Notifications** through any shoutrrr target, filtered by change kind *and* severity.
- **Backups that are actually consistent** — one endpoint, one file, safe to take while Silt
  is running.

**[Everything else is in the wiki →](https://github.com/unmaykr-a/silt/wiki)**

<br />

## Screenshots

<div align="center">
<table>
<tr>
<td valign="top" width="50%"><img src="docs/screenshots/projects.png" alt="Projects" width="440" /><br /><sub><b>Projects</b> — what is running, what is broken, what was edited and never applied.</sub></td>
<td valign="top" width="50%"><img src="docs/screenshots/diff.png" alt="Diff" width="440" /><br /><sub><b>Diff</b> — one <code>compose up</code>: an upgrade, a rotated key, a new setting.</sub></td>
</tr>
<tr>
<td valign="top" width="50%"><img src="docs/screenshots/files.png" alt="Compose file diff" width="440" /><br /><sub><b>Compose files</b> — the changed line, with the secret still a digest.</sub></td>
<td valign="top" width="50%"><img src="docs/screenshots/service.png" alt="Service history" width="440" /><br /><sub><b>Service history</b> — images, restarts, and when each value last changed.</sub></td>
</tr>
</table>
</div>

<br />

## Quick start

```bash
curl -O https://raw.githubusercontent.com/unmaykr-a/silt/main/docker-compose.yml
docker compose up -d
```

Then open `http://your-host:8375` and choose a password. That is the whole install: one
static binary with the UI embedded, one SQLite file, and a read-only Docker socket proxy.

The socket proxy in that compose file is not optional decoration — mounting
`/var/run/docker.sock:ro` into Silt directly would **not** be a security boundary, because
read-only applies to the file and not to the API. [The wiki explains
why](https://github.com/unmaykr-a/silt/wiki/Installation#why-the-socket-proxy-is-not-decoration).

To also capture the compose files themselves, mount your compose directories read-only and
allowlist them:

```yaml
environment:
  SILT_COMPOSE_ROOTS: /srv,/opt
volumes:
  - /srv:/srv:ro
  - /opt:/opt:ro
```

<br />

## Documentation

Everything lives in the **[wiki](https://github.com/unmaykr-a/silt/wiki)**:

| | |
|---|---|
| [Install](https://github.com/unmaykr-a/silt/wiki/Installation) | The compose file, the socket proxy, reverse proxies |
| [Configuration](https://github.com/unmaykr-a/silt/wiki/Configuration) | Every setting, with defaults |
| [Authentication](https://github.com/unmaykr-a/silt/wiki/Authentication) | OIDC, forward auth, the built-in account, roles |
| [Secrets and redaction](https://github.com/unmaykr-a/silt/wiki/Secrets-and-Redaction) | The threat model, and what is *not* protected |
| [Backups](https://github.com/unmaykr-a/silt/wiki/Backups) | Why `cp silt.db` is wrong |
| [Troubleshooting](https://github.com/unmaykr-a/silt/wiki/Troubleshooting) | The real failure modes |
| [HTTP API](https://github.com/unmaykr-a/silt/wiki/API) | Endpoints, SSE, the OpenAPI contract |

The wiki pages are in [`docs/wiki/`](docs/wiki) and published by CI, so they change with the
code that changes their meaning.

[`PROJECT.md`](PROJECT.md) is the design brief: every decision, and more usefully every
decision that changed during implementation and why — the mistakes as well as the choices.

<br />

## Platforms

`linux/amd64` and `linux/arm64`. There is **no `linux/arm/v7` build**: the pure-Go SQLite
driver that lets Silt ship as a static binary with no CGO does not support 32-bit ARM well
enough to trust with your history. A Pi 4 or 5 on a 64-bit OS is where it was developed.

<br />

## What Silt is not

- **Not a deployment tool.** It observes. Rollback, if it ever exists, means "here is the
  old compose file, go apply it yourself".
- **Not a monitoring system.** It consumes health signals; it does not probe.
- **Not a log aggregator.** Container logs are out of scope.
- **Not Kubernetes.** Compose only.

<br />

## Status

**1.0** — everything in the v1 scope is built, and the HTTP API and database schema are
stable: `api/openapi.yaml` is the contract, a test asserts the handlers still match it, and
migrations only ever move forward.

What 1.0 does not claim is a long operational history. Silt is built for one operator and
one Docker host, it is tested hard — the suite includes a sentinel test that plants a
secret-shaped string in every field and then byte-scans the database file, its write-ahead
log, every decompressed blob and the debug logs — and it has been running on a Raspberry Pi
behind a reverse proxy for as long as it has existed. Somewhere between those two facts is
the honest answer. Issues welcome.

<br />

## Developing

```bash
make check    # the gate: build, vet, Go tests, frontend tests and build
make e2e      # browser checks against a real binary and a seeded database
```

See [Development](https://github.com/unmaykr-a/silt/wiki/Development) for the layout, the
make targets, and the conventions worth knowing before sending a patch.

<br />

## License

AGPL-3.0-or-later. Copyright (c) 2026 unmaykr-a. See [`LICENSE`](LICENSE).

The gap Silt fills is a paywalled feature elsewhere; the licence is chosen to keep it from
becoming one again.

<br />

## Supporting Silt

Silt is free and AGPL-3.0 licensed, and always will be. If it has saved you an evening of
"what changed?", [a coffee](https://ko-fi.com/unmaykr) is a kind way to say so — and never
required.
