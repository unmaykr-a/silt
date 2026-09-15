# Silt

A self-hosted change journal for Docker Compose stacks. **What settled on your stack, and when.**

Silt watches your Docker host and records, over time, the effective configuration of every
Compose project, the resolved image identity of every service, container state, and a stream
of events — so you can answer one question well: **what changed, and when?**

When something breaks at 03:10, you can see the image that got pulled at 03:00.

> **[Try the live demo →](https://unmaykr-a.github.io/silt/)** The whole UI in your browser,
> against a made-up host. Nothing to install.

---

## Start here

| | |
|---|---|
| **[Install](Installation)** | A compose file you can paste, and why the socket proxy is not optional. |
| **[Configuration](Configuration)** | Every environment variable, with defaults. |
| **[Authentication](Authentication)** | Three ways in, roles, and the sharp edges. |
| **[Backups](Backups)** | Do not copy `silt.db`. Here is what to do instead. |

## The one architectural rule

**Silt never writes to the Docker API.** It observes, through a read-only socket proxy. That
is a design rule rather than a v1 shortcut: there is no code path that starts, stops or
changes a container, and there is not meant to be one. See [How it works](Architecture).

## What it is not

- **Not a deployment tool.** It records; it does not apply. Nothing here will `up` a stack.
- **Not a monitoring system.** It has no opinion about whether your CPU is busy. It records
  configuration and state changes, and accepts events from the tools that do monitor.
- **Not multi-host.** One Silt watches one Docker host. The schema is shaped for more, and an
  agent binary is a v2 idea, not a v1 one.
- **Not a secrets store.** The opposite: it is built so that a copy of its database is not a
  copy of your secrets. See [Secrets and redaction](Secrets-and-Redaction).

## Everything else

- **What Silt records** — [How it works](Architecture) ·
  [Compose file capture](Compose-File-Capture) ·
  [Secrets and redaction](Secrets-and-Redaction) · [Retention](Retention) ·
  [The screens](The-Screens)
- **Set-up guides** — [Authentication](Authentication) · [Notifications](Notifications) ·
  [Webhook ingest](Webhook-Ingest) · [Backups](Backups) ·
  [Metrics and health](Metrics-and-Health)
- **Reference** — [HTTP API](API) · [Troubleshooting](Troubleshooting) · [FAQ](FAQ) ·
  [Development](Development) · [Upgrading](Upgrading)

For the design brief — every decision and, more usefully, every decision that changed during
implementation and why — see
[`PROJECT.md`](https://github.com/unmaykr-a/silt/blob/main/PROJECT.md) in the repository.
