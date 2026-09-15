# Install

Silt is one static binary with the UI embedded, published as a multi-arch image. There is
nothing to install beside it — no database server, no message queue, no sidecar except the
socket proxy, which is not optional and is explained below.

- **Image:** `ghcr.io/unmaykr-a/silt`
- **Platforms:** `linux/amd64`, `linux/arm64` (no `armv7` — see [FAQ](FAQ))
- **Storage:** one SQLite file

## A compose file you can paste

```yaml
services:
  silt:
    image: ghcr.io/unmaykr-a/silt:1
    container_name: silt
    restart: unless-stopped
    ports:
      - "${SILT_PORT:-8375}:8375"
    environment:
      SILT_DOCKER_HOST: tcp://docker-socket-proxy:2375
    env_file:
      - .env
    volumes:
      - silt-data:/data
    depends_on:
      - docker-socket-proxy

  docker-socket-proxy:
    image: lscr.io/linuxserver/socket-proxy:latest
    container_name: docker-socket-proxy
    restart: unless-stopped
    environment:
      # Everything Silt reads.
      CONTAINERS: 1
      IMAGES: 1
      EVENTS: 1
      VERSION: 1
      INFO: 1
      # Everything it must never be able to do.
      POST: 0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    read_only: true
    tmpfs:
      - /run

volumes:
  silt-data:
```

Then:

```bash
docker compose up -d
```

Open `http://your-host:8375`. On first run Silt asks you to choose a password, and refuses
every other request until you do — see [Authentication](Authentication).

## Why the socket proxy is not decoration

Mounting `/var/run/docker.sock:ro` into Silt directly would **not** be a security boundary.
Read-only applies to the *file*, not to the API: anything holding that socket can still
create a privileged container and own the host. `:ro` on a Docker socket buys you nothing.

The proxy enforces read-only where it actually means something — at the HTTP verb level, with
`POST=0` — and it lets Silt run as a non-root user with no membership of the `docker` group.
Silt has no code that writes to the Docker API, and the proxy is what makes that
architectural claim true rather than a promise.

## Capturing the compose files themselves

By default Silt records the *running* configuration, read from the Docker API. To also capture
the compose and `.env` files on disk — which is what lets it show you which line changed, and
notice an edit you never applied — mount your compose directories read-only **at the same
paths they have on the host**, and allowlist them:

```yaml
environment:
  SILT_COMPOSE_ROOTS: /srv,/opt
volumes:
  - /srv:/srv:ro
  - /opt:/opt:ro
```

The paths must match the host's because the paths Silt follows come from container labels,
which record where the files are on the host.

`SILT_COMPOSE_ROOTS` is an **allowlist, not a hint.** Anyone who can start a container can
set those labels, so nothing outside these roots is ever read — or watched — symlinks
included. With roots set, Silt also watches those files, so an edit is on the timeline within
a second of the save. See [Compose file capture](Compose-File-Capture).

## Behind a reverse proxy

Silt is a plain HTTP server and expects to sit behind your own proxy. Two things matter:

**Server-sent events.** The live timeline holds a long connection open. Silt sets
`X-Accel-Buffering: no`, which nginx honours, but set the directive too if you have one:

```nginx
location / {
    proxy_pass http://silt:8375;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;            # SSE
    proxy_read_timeout 1h;          # the stream is meant to be long-lived
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;   # see below
}
```

**`X-Forwarded-Proto`.** Silt uses it to decide whether the session cookie gets the `Secure`
flag. If your proxy terminates TLS and does not set this header, Silt sees plain HTTP and
sends the cookie without `Secure`. Either set the header, or set
`SILT_COOKIE_SECURE=always` — or just set `SILT_BASE_URL` to your `https://` address, which
counts as an answer.

Also set `SILT_BASE_URL` so notifications can link back to the change they are about, and so
the OpenID Connect callback can be derived.

## Next

- [Configuration](Configuration) — every setting
- [Authentication](Authentication) — before you expose it to anything
- [Backups](Backups) — because `cp silt.db` is wrong in a way that does not announce itself
