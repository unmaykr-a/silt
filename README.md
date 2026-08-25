# Silt

*What settled on your stack, and when.*

A self-hosted change journal for Docker Compose stacks. Silt records the effective
configuration, resolved image identity and container state of every Compose project on a
Docker host, then lets you diff any two points in time — so when something breaks at
03:10 you can see the image that got pulled at 03:00.

Silt **never writes to the Docker API.** It observes, through a read-only socket proxy.

> **Status: pre-alpha.** Nothing works yet. See [`PROJECT.md`](PROJECT.md) for the full
> design brief and milestone plan.

## What Silt stores

Silt reads your Compose environment, so it is built to never persist a recoverable
secret. Environment values are redacted by default and kept in cleartext only for an
explicit list of known-safe keys (`PUID`, `TZ`, `LOG_LEVEL`, …). Redacted values are
recorded as a truncated HMAC under a random key generated on first boot and never
exported, so change detection works while the stored digests are useless to anyone
holding the database. Compose `secrets:` and `configs:` are recorded by name and mount
target only, never by content. Full detail in Section 7 of `PROJECT.md`.

## Platforms

`linux/amd64` and `linux/arm64`. There is no `linux/arm/v7` build and there are no plans
for one.

## License

AGPL-3.0-or-later. Copyright (c) 2026 unmaykr-a. See [`LICENSE`](LICENSE).
