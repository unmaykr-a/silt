# Authentication

Silt has three ways in, and they compose. A fresh install is **closed**: it asks you to choose
a password the first time you open it and refuses every other request until you do.

| Method | Set with | Groups re-read |
|---|---|---|
| Built-in account | on by default; `SILT_PASSWORD_HASH` to claim it at startup | n/a |
| OpenID Connect | `SILT_OIDC_ISSUER` + client | at sign-in |
| Forward auth | `SILT_TRUST_PROXY_AUTH` + `SILT_TRUSTED_PROXIES` | every request |

More than one can be on at once. With a provider configured, the login screen offers a button
for it *and* a password box, and either gets you in — which is what you want on the day the
provider is the thing that is down.

## Changing it without a restart

Since 1.2.0 all of this is editable under **Settings → Authentication**, not only in the
compose file. Saving rebuilds the whole gate: the built-in account is re-read, the forward-auth
rules are rebuilt, and provider discovery runs again. Sessions survive it, because they are rows
in Silt's database rather than signed cookies — shortening a lifetime re-dates the sessions that
exist rather than ending them.

This was deliberately withheld before 1.2.0, on the argument that a UI able to edit the boundary
in front of it would be a way in rather than a setting. What changed the answer is that only an
administrator reaches that screen, and an administrator already holds the stronger controls: the
whole-database backup, the password, and every session. Withholding it bought nothing and cost a
container recreate to fix a mistyped issuer.

Two things follow, and both matter more than the convenience:

**`SILT_PASSWORD_HASH` is still not editable.** The variable exists to take the password out of
the UI's hands for an install managed from a compose file, so a UI that could set it would
defeat its only purpose. The settings screen reports whether it is set and nothing more.

**You can lock yourself out.** A provider that has moved, a typo in an allowlist, the local
account turned off beside a provider that has stopped answering — any of these leaves an install
nobody can sign in to. The way back is:

```yaml
environment:
  SILT_SETTINGS_RESET: "true"
```

Recreate the container. Every setting saved from the UI is dropped and Silt runs exactly what its
environment says, so you are back to your compose file. **Unset it afterwards**, or the next
restart drops your settings again.

A provider Silt cannot reach is not a lockout: that login is disabled, the reason is shown on the
settings screen, and every other way in keeps working. The save is accepted so you can correct
the issuer from the same screen.

## The built-in account

There is one, and it has a `CHECK (id = 1)` constraint behind it. A table of users would be a
user system nobody asked for; identity comes from your provider.

- **First run** — Silt serves only the setup form until a password is set.
- **`SILT_PASSWORD_HASH`** — claims it before Silt ever starts, so the first-run window never
  exists. Generate with `htpasswd -bnBC 12 "" 'your password' | tr -d ':\n'`, and note the
  doubled `$` if you put it in a compose file, because compose eats single ones.
- **`SILT_LOCAL_ACCOUNT=false`** — turns it off entirely, for an install that authenticates
  only through a provider or a proxy. Silt refuses to turn it off while it is the only way in.
- **Linking** — you can link the account to a provider identity, so signing in there reaches
  the same account. That is what lets you turn the password off and keep the account.

Sign-in attempts are throttled per client address, with free attempts first: typing a password
wrong twice is normal.

## OpenID Connect

Works with authentik, Authelia, Keycloak, Pocket ID — anything standard.

```yaml
SILT_OIDC_ISSUER: https://auth.example.lan/application/o/silt/
SILT_OIDC_CLIENT_ID: silt
SILT_OIDC_CLIENT_SECRET: ...
SILT_OIDC_ALLOWED_GROUPS: silt-users
SILT_OIDC_ADMIN_GROUPS: silt-admins
```

Register the callback with your provider as `<where Silt is>/api/auth/callback`.

> **Paste the issuer exactly as your provider prints it, trailing slash included.** The
> library compares the issuer in the discovery document against the string you gave it,
> character for character. authentik publishes its issuer with a trailing slash, and that is
> also how it prints it for you to copy. Normalising it turns "paste the URL authentik shows
> you" into a silent discovery failure.

Both allowlists empty admits anyone your provider will authenticate. That is the right default
for a provider with only your accounts on it, and wrong for a shared one.

## Forward auth

If your reverse proxy already authenticates — Authelia, authentik's proxy provider, tinyauth —
Silt can believe the identity it asserts.

```yaml
SILT_TRUST_PROXY_AUTH: "true"
SILT_AUTH_HEADER: X-Remote-User
SILT_TRUSTED_PROXIES: 172.18.0.0/16
```

> **Set `SILT_TRUSTED_PROXIES`.** The header is settable by anything that can open a socket.
> Without a trust list, "authenticated" means "reached the port" — and on a shared Docker
> network, that is every other container on it. The Setup section of the settings screen
> reports this as an error, not a warning.

## Roles

`SILT_OIDC_ADMIN_GROUPS`, or `SILT_ADMIN_GROUPS` behind a forward-auth proxy, splits reading
the journal from changing Silt's own configuration.

- **Administrator** — everything.
- **Viewer** — reads every screen, changes nothing except their own appearance preferences,
  which live in their browser anyway. Cannot download the database.

Unset means everyone admitted is an administrator, which is what Silt did before roles
existed. Turning an upgrade into a lockout for the person who configured it would be the worst
possible default.

There is no roles table. Your provider already manages groups, and duplicating them here would
be two sources of truth that agree until they do not.

### The one asymmetry worth knowing

A provider's groups are read **once, at sign-in**, and nothing re-reads them — there is
nothing to re-read them from without storing a refresh token. So removing someone from the
administrator group is not instant.

`SILT_OIDC_ADMIN_TTL` (default `12h`) bounds it: after that window the session keeps working,
**read-only**, until they sign in again. Only the administrator half lapses, and only for
provider sessions. Forward auth asserts its groups on every request and is never stale; the
built-in account has no provider to have changed its mind.

To make a demotion immediate, end their session: Settings → Security → **Sign out
everywhere**.

## Sessions

Rows in Silt's database, not signed cookies. That means they survive a restart, signing out
actually revokes one server-side, and a cookie a browser threw away is not still a working
credential to anyone who copied it.

- `SILT_SESSION_TTL` (`720h`) — absolute lifetime.
- `SILT_SESSION_IDLE_TTL` (`168h`) — ends an unused one early.
- Only a SHA-256 digest of the token is stored, so a copy of the database is not a set of
  working sessions.
- **Sign out everywhere** ends every session including yours. It is the button for "I think a
  token leaked".

Changing the password signs every other browser out, so doing it because you think it leaked
also ends whatever leaked.

## What protects the rest

- **CSRF** — checked on `Sec-Fetch-Site`/`Origin`, before authentication and whether or not
  authentication is on, because an open install still has state-changing endpoints.
- **CSP** — `script-src 'self'`, no `unsafe-inline` for scripts, `frame-ancestors 'none'`.
- **Cookies** — `HttpOnly`, `SameSite=Lax` (required: the OIDC callback is a top-level
  navigation), `Secure` per `SILT_COOKIE_SECURE`.
- **`/healthz` and `/readyz`** stay reachable whatever the authentication, so your orchestrator
  does not need a credential.

## The activity trail

Settings → Security lists who changed a setting, who ran a prune, who signed in and who was
refused. It records **what** changed and never what it changed to — a table built to be read
should not be where an ingest token ends up.

Kept for `SILT_AUDIT_RETENTION_DAYS` (default 730), separate from the event window, because
its entire value is how far back it reaches.
