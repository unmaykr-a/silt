# Silt

*What settled on your stack, and when.*

A self-hosted change journal for Docker Compose stacks. Silt records the effective
configuration, resolved image identity and container state of every Compose project on a
Docker host, then lets you diff any two points in time — so when something breaks at
03:10 you can see the image that got pulled at 03:00.

Silt **never writes to the Docker API.** It observes, through a read-only socket proxy.

> **Status: pre-alpha, under active construction.** Silt currently discovers your
> Compose projects and reports coalesced changes to its log; it does not yet store
> history, diff snapshots, or show you anything beyond a status page. Follow
> [`PROJECT.md`](PROJECT.md) for the design brief and milestone plan.

## Running it

```bash
curl -O https://raw.githubusercontent.com/unmaykr-a/silt/main/docker-compose.yml
docker compose up -d
```

Then open `http://<host>:8375`.

The socket proxy in that file is not optional decoration. Mounting
`/var/run/docker.sock:ro` into Silt directly would not be a security boundary:
read-only applies to the file, not to the API, so anything holding it can still
create privileged containers. The proxy enforces read-only at the HTTP verb
level with `POST=0`.

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
