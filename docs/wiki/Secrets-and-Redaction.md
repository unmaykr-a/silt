# Secrets and redaction

Silt reads your Compose environment, so it is built never to persist a recoverable secret.

**The threat model is explicit: someone obtains `silt.db`.** A leaked backup, a misconfigured
volume, a shared debug bundle, a stolen laptop. Everything below follows from assuming that
happens.

## Keep-list, not redact-list

Every environment value is redacted **by default**. Cleartext is kept only for keys on an
explicit safe list — `PUID`, `PGID`, `TZ`, `LOG_LEVEL`, `*_PORT` and similar — which you can
extend with `SILT_KEEP_KEYS`.

This is the whole design. A "redact these patterns" list has a failure mode where the pattern
did not anticipate `DB_PASS_2` or `MY_TOKEN_V2`, and the value is stored in cleartext forever
with nothing to indicate it. A keep-list fails the other way: an unrecognised key is redacted,
which is annoying and safe. There is no `SILT_REDACT_KEYS` and there will not be.

```yaml
SILT_KEEP_KEYS: MY_APP_REGION,APP_*
```

A key is a name, optionally with a single `*` at one end. Anything that would keep more than it
names — `*` on its own, `[A-Z]*` — is **refused at startup**, because it would store every
environment variable on the host, passwords included, in cleartext.

## Silt's own credentials

Redaction is for values Silt *observed*. It keeps nothing recoverable, because it never needs
to: a digest answers "did this change?" and nothing else is asked of it.

Silt's own settings are different. The ingest token, the shoutrrr notification targets and the
OpenID Connect client secret are credentials Silt has to be able to **use** — it must present
the real client secret to your provider — so they cannot be digested. They live in Silt's
settings row, and until 1.2.0 they lived there in plaintext.

```yaml
SILT_SECRET_KEY: any-length-string-kept-here-and-nowhere-else
```

With it set, those three are encrypted at rest with AES-256-GCM. The key stays in the
environment and is never written to the database or included in a backup, so `GET /api/backup`
— which is a copy of the database file, and a file people keep — yields ciphertext.

Without it, behaviour is exactly what it was, because a key that is *required* breaks every
install that upgrades without setting one. Adding one later works: values stored before it are
read as they are, and re-written encrypted on the next save.

Removing the key after using it is reported rather than survived. Silt would otherwise present
`enc:v1:…` to your provider as a client secret and report an authentication failure, which sends
you to read your provider's logs instead of your own compose file.

This is a weaker guarantee than redaction and worth being clear about: it is only as good as
keeping the key out of the database. Redaction cannot be reversed even with the key, because
there is nothing to reverse.

## What a redacted value looks like

A truncated HMAC-SHA256 under a random key generated on first boot, stored in the database and
never exported:

```
RADARR__API_KEY=[redacted:8f14e45fceea]
```

A bare hash would be a guessing oracle — a four-digit PIN is ten thousand hashes, and a
short password is a wordlist away. The per-install key makes the digest useless to anyone
holding the database, while still changing when the value changes. Which is the point: the
history can tell you **when `API_KEY` changed** without ever holding the key.

Only a **length bucket** is stored, never the exact length.

## The rest of the rules

- **Bind mount source paths are redacted.** Type, target, mode and named-volume names are kept
  — a path on your host is information about your host.
- **Compose `secrets:` and `configs:`** are recorded by name and mount target only, never by
  content.
- **Notification targets are masked when read back.** A shoutrrr URL carries the credential for
  the service it points at, so the settings screen shows the scheme and, where it is a real
  host rather than an identifier, the host — never the token.
- **The ingest token is never returned**, only reported as set or not.
- **Search never matches values.** It searches project names, service names, environment
  variable *names*, file paths and event text. Searching a secret finds nothing, by
  construction.
- **The audit trail records field names, never values.**

## Compose files, line by line

Captured files go through the same redaction, value by value, with every comment, indent and
image tag left exactly as written:

```yaml
environment:
  - PUID=1000
  - TZ=Europe/Tallinn
  - RADARR__API_KEY=[redacted:8f14e45fceea]
```

Line structure is preserved so a line diff still answers the question people actually ask —
which line changed. A rotated key is a visibly changed line and nothing more.

`.env` files get the same treatment, and they are the case this matters most for, because a
`.env` is nothing but secrets.

## Choosing what to hide

The built-in safe list is a guess, and guesses are wrong sometimes. On any captured file you
can click a line to correct it in either direction:

- **Hide** a line the keep-list let through.
- **Reveal** one it redacted unnecessarily.

Hiding takes effect **before anything is written**, so a hidden value is never stored.
Revealing applies only to **future** captures: earlier snapshots hold a digest, not the value,
so there is nothing there to uncover. That asymmetry is deliberate and the UI says so.

A rule with an empty path applies to every file in the project, which is how you hide
`SMTP_PASSWORD` everywhere at once.

## The test that keeps this honest

A test plants a sentinel string in every secret-shaped field — environment values, bind mount
sources, compose file contents, secret names, labels, command arguments — runs a full snapshot
write plus a prune and blob garbage collection, then **byte-scans the database file, its
write-ahead log, every decompressed blob and the captured debug logs** for that sentinel.

If it appears anywhere, the test fails. It runs in CI on every push.

That is the difference between a claim and a property. Redaction that is correct because
someone read the code carefully is correct until the next change; redaction that is checked by
scanning the actual bytes on disk stays correct.

## What is *not* protected

Being straight about the boundaries:

- **Project and service names are cleartext.** They have to be — they are the index.
- **File paths of captured compose files are cleartext.**
- **Event messages are stored as received.** If you `POST` a secret to the ingest webhook, Silt
  stores it. Do not do that.
- **A cleartext value you added to the keep-list is cleartext.** That is what you asked for.
- **Silt is not an encrypted store.** The database is not encrypted at rest; redaction is about
  what gets written, not about protecting the file. Put it on an encrypted volume if that
  matters to you.
