# FAQ

### Will Silt change anything on my host?

No, and it has no code that could. It never writes to the Docker API, and the socket proxy with
`POST=0` is what makes that an enforced boundary rather than a promise. It is a journal, not a
deployment tool.

### Why not just mount the Docker socket read-only?

Because `:ro` on a Docker socket is not a security boundary. Read-only applies to the *file*, not
to the API — anything holding that socket can still create a privileged container and own the
host. The proxy enforces read-only where it means something, at the HTTP verb level.

### Does it work without the compose files?

Yes. Silt records the running configuration from the Docker API, which is the primary source.
Mounting the files adds which *line* changed and lets it notice an edit you never applied.

### Can it watch more than one Docker host?

Not in 1.0. One Silt watches one host. The schema is shaped for more — hosts are first-class
rows — and a lightweight agent binary is a v2 idea.

### Will it store my secrets?

It is built so a copy of its database is not a copy of your secrets. Values are redacted by
default and kept in cleartext only for keys on an explicit safe list; redacted values are a
truncated HMAC under a per-install key, so a changed secret is a visibly changed digest and
nothing recoverable. A test plants sentinels and byte-scans the database, its WAL, every blob and
the logs on every CI run. See [Secrets and redaction](Secrets-and-Redaction).

### Why a keep-list instead of "redact these patterns"?

Because a pattern list fails silently in the dangerous direction. The day something is named
`DB_PASS_2` and the regex did not anticipate it, the value is in cleartext forever with nothing
to indicate it. A keep-list fails the other way: an unrecognised key is redacted, which is
annoying and safe.

### Can I un-redact something?

You can mark a line to reveal, and it applies to **future** captures. Earlier snapshots hold a
digest rather than the value, so there is nothing there to uncover. Hiding, conversely, takes
effect before anything is written.

### Why is there no `armv7` build?

`modernc.org/sqlite` — the pure-Go SQLite that lets Silt ship as a static binary with no CGO —
does not support 32-bit ARM well enough to trust with your history. `linux/amd64` and
`linux/arm64` are the platforms. A Pi 4 or 5 on a 64-bit OS is fine; that is where it was
developed.

### How much disk does it use?

Much less than people expect. Blobs are content-addressed, so forty services on the same base
image share one. An observation identical to the previous one updates that row rather than
inserting, so an idle hour of five-minute snapshots across forty services costs zero bytes.
Events are the thing that grows; they have their own retention window for that reason.

### Does it need a database server?

No. One SQLite file, WAL mode, pure-Go driver. Nothing else to run.

### Is the API stable?

Yes, as of 1.0. `api/openapi.yaml` is the contract and a test asserts the handlers match it in
both directions. Migrations only ever move forward.

### Can I use it with Watchtower?

Yes, and it is a good pairing — Watchtower produces image changes constantly and Silt is the
record of which one broke things. Expect more `image_id` and `image_digest` changes than a
hand-managed host, and tune `SILT_NOTIFY_ON` accordingly.

### Does it replace my monitoring?

No. It has no opinion about whether your CPU is busy. It records configuration and state
changes, and accepts events from the tools that *do* monitor, so a probe failure sits on the same
axis as the image that got pulled five minutes earlier. See
[Webhook ingest](Webhook-Ingest).

### Why is the settings screen read-only in places?

Because some settings are the boundary protecting that screen. A UI that could turn off the login
in front of it, or widen which files Silt reads, would be a way in rather than a setting. Those
are environment-only and say so.

### Can two people use it?

Yes: an identity provider or forward auth, with `SILT_OIDC_ADMIN_GROUPS` splitting administrator
from read-only. There is no user table — identity comes from your provider, and duplicating its
groups here would be two sources of truth that agree until they do not. Per-project visibility is
deliberately not built; see `PROJECT.md` section 14.

### Why AGPL?

The gap Silt fills is currently a paywalled feature elsewhere, and AGPL keeps it from becoming
one again. Note the asymmetry: MIT → AGPL is a decision you can make unilaterally, while
AGPL → MIT later needs every contributor's consent. So it was chosen deliberately at
zero-contributor time.

### What does "silt" mean here?

What settles, in layers, over time — and what you dig through to see what was there before.
