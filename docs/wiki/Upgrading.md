# Upgrading

```bash
docker compose pull silt
docker compose up -d silt
```

Migrations run forward at startup. There is nothing else to do.

## Versioning

Silt follows semantic versioning from 1.0.

- **Patch** — fixes, no behaviour changes you did not ask for.
- **Minor** — new features, new settings with defaults that preserve current behaviour.
- **Major** — a breaking change to the HTTP API, the database schema's forward compatibility, or
  a default that changes what Silt does.

Pin however you like. `:1` tracks the 1.x line, `:1.0.0` pins exactly, `:latest` follows main.

## What 1.0 promises

- **The HTTP API is stable.** `api/openapi.yaml` is the contract and a bidirectional test asserts
  the handlers still match it — every documented operation reachable and shaped as declared,
  every registered route documented. Drift fails CI.
- **Migrations only ever move forward.** There is no down path in a tool whose value is a history
  you cannot reconstruct. A newer Silt reads an older database; the reverse is not supported.
- **Settings keep their meaning.** A new setting arrives with a default that preserves what your
  install already does. When that was impossible the changelog says so explicitly — see the
  0.16.0 note about `/metrics` becoming authenticated by default.

## What it does not promise

A long operational history. Silt is built for one operator and one Docker host, it is tested
hard, and it has been running on a Raspberry Pi behind a reverse proxy for as long as it has
existed. Somewhere between those two facts is the honest answer.

## Before a major upgrade

Take a backup. It is one request:

```bash
curl -fsSL --cookie "silt_session=$TOKEN" \
  https://silt.example.lan/api/maintenance/backup -o silt-before-upgrade.db
```

See [Backups](Backups) — and note that copying `silt.db` is not the same thing and will bite you.

## Downgrading

Not supported, because migrations do not go backwards. If you need to, restore the backup you
took before upgrading.

## Reading what changed

[`CHANGELOG.md`](https://github.com/unmaykr-a/silt/blob/main/CHANGELOG.md) is generated from the
same source the in-app version dialog renders, so they cannot disagree. The version button in the
header opens it.

For *why* something changed, `PROJECT.md` keeps a numbered list of every decision that moved
during implementation — including the four features that were documented and not built, and what
finally caught them.
