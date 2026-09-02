# Changelog

All notable changes to Silt are recorded here.

This file is generated from internal/changelog/changelog.go — edit that and run
`make changelog`.

## 0.7.0 — 2026-09-02

The Projects screen says what needs you.

### Added

- A summary strip above the grid, where every count is a filter. Seeing "3 unhealthy" and then having to hunt for which three was the thing this screen was worst at.
- Unapplied compose edits are a state, not just an event. Silt has always recorded config.drift when a file changed without the stack changing, but the event scrolls off the timeline while the file stays un-applied. The Projects screen answers whether it is still true, by comparing the files on disk against the ones in place at the last actual change.
- A "send a test" button for notification targets. A shoutrrr URL is wrong until something tries to send, and the only thing that tries to send is the change that mattered — so the first proof that notifications work used to be the outage they were configured for. Each target is reported separately, because the useful answer is which one is broken.

### Changed

- Projects was a card per stack carrying its name and when it was last seen — on a host running forty of them, forty cards all saying "2m ago". Each card now says what is running, what is unhealthy, what has been restarting, and what was edited but never applied, and the default order puts the broken ones first.

### Fixed

- Truncating a message for a notification cut on bytes rather than characters, which could split a multi-byte character into replacement glyphs.

### Security

- Notification test failures are masked before they are shown. Providers quote the request URL back in their error text and a shoutrrr URL is a credential, so every fragment of the target is stripped from the message before it reaches the screen.

## 0.6.0 — 2026-09-02

Find anything, and a service page worth opening.

### Added

- Search across every project, service, environment key, compose file and event. Press / from anywhere. On a host with forty-odd stacks, remembering which project a container belongs to was the slowest part of using Silt.
- Search matches environment variable names, never their values. A redacted value is not searchable by anyone, including whoever is signed in.
- A diff can leave Silt: Markdown from the structured view for pasting into an issue, and from the YAML view a real unified diff that patch(1) and git apply both accept — applying it turns the older compose document into the newer one. A change no longer has to travel as a screenshot.

### Changed

- The service page was a list of observations; it is now a history. What the service is right now, every image it has run with how long it held, a link from each change straight to the diff that introduced it, restarts over time, and the environment keys that changed.

### Fixed

- A locally built image has no registry digest, and its image ID was being labelled as one. The two are different claims and the page now says which it is showing.

## 0.5.1 — 2026-08-26

OpenID Connect actually works with authentik.

### Changed

- With a provider configured, a fresh install no longer forces you through local setup first. Sign in with the provider; add a password later under Settings → Security, or never.

### Fixed

- An issuer with a trailing slash — which is how authentik publishes and prints its own — failed discovery, so the provider silently did not appear on the login screen. Silt was normalising the URL before handing it over, and the comparison is character for character.
- SILT_BASE_URL is no longer required to use a provider. The callback is derived from the request when nothing is configured, which behind a reverse proxy is the public name a browser actually used. Set it, or SILT_OIDC_REDIRECT_URL, to pin it.
- A provider that is configured but unreachable now says so on the login screen and under Settings → Security, with the reason. It used to just not appear, which sends you to the wrong place to debug it.

### Security

- Claiming the built-in account anonymously is only possible when it is the only way in. Once a provider or a proxy could admit someone, an anonymous claim would be taking an account that bypasses them, so it requires a session.

## 0.5.0 — 2026-08-26

A closed door on first boot, and a compose file short enough to read.

### Added

- The built-in account can be renamed nothing and moved nowhere, but it can be managed: change its password, link it to a provider identity so signing in there reaches the same account, or turn it off once something else can let you in.
- SILT_PASSWORD_HASH now claims the account before Silt starts, which removes the first-run window entirely for anyone managing Silt declaratively. The UI then reports the password as the environment's and does not offer to change it.
- PNG renders of the mark beside the favicon, at 512 and 1024, light and dark, plus a padded tile.

### Changed

- The compose file is short enough to read. Everything optional moved to .env, with a documented .env.example covering every setting and saying which ones need a restart.
- Opening a project's compose files shows the whole file first. You are usually there to read the compose; the timeline already answers what changed, and links straight to the diff.

### Fixed

- The login screen was half-built: no way to start an OpenID Connect sign-in, and a password box on an install with no password. It now offers exactly what is configured, and says so when the answer is "your reverse proxy should have done this".
- Reloading while signed out flashed the whole application before replacing it with the login form. Silt now waits until it knows whether it is locked.

### Security

- A fresh install is closed, not open. The built-in administrator exists from first boot, and the first thing Silt asks for is a password — until then every request is refused. The old default made the safe configuration the one you had to know to ask for.
- Changing the password revokes every other session, so doing it because you think one leaked also ends whatever leaked.

## 0.4.0 — 2026-08-26

Sign in with your identity provider, and a security pass over everything around it.

### Added

- OpenID Connect login against any provider — authentik, Authelia, Keycloak, Pocket ID, Google. Authorization code flow with PKCE, a verified id_token, and optional group and user allowlists.
- Sessions are rows in Silt's database rather than signed cookies. They survive a restart, signing out revokes them server-side, and "sign out everywhere" ends all of them at once.
- A Security section on the settings screen: what is protecting this install, which provider, how many sessions exist, and the button to end them.

### Security

- Forward auth now believes the identity header only from addresses on SILT_TRUSTED_PROXIES. Without that list anything able to reach the port could claim to be anyone — which on a shared Docker network is every other container on it. An empty list still trusts any source, and now says so loudly at startup.
- /metrics requires authentication by default. It names every project on the host and counts its changes, which is not something to hand out because a scrape is easier without a token. SILT_METRICS_PUBLIC brings the old behaviour back.
- Unsafe requests from another origin are refused, so a page elsewhere cannot drive Silt through a signed-in browser.
- Content-Security-Policy, X-Frame-Options, X-Content-Type-Options, Referrer-Policy and Permissions-Policy on every response. Scripts may only come from Silt itself.
- Failed password attempts back off per client, after a few free tries so a typo costs nothing. bcrypt handles the offline attack; this handles the online one.
- The session cookie is marked Secure when the request arrived over TLS, directly or through a proxy that says so — rather than never, as before.
- The post-login destination is reduced to a same-origin path, so the login flow cannot be turned into an open redirect.

## 0.3.0 — 2026-08-26

Compose files you can read, a timeline you can scan, and a layout you can choose.

### Added

- Syntax highlighting for compose files, everywhere they are shown. Keys, strings, numbers, comments, anchors and ${VAR} references each get their own colour, and Silt's own redaction placeholder gets one too so a hidden value is visible as hidden.
- Comparing two snapshots as YAML now marks what actually changed: changed lines are tinted, the changed words inside them are highlighted, and the unchanged runs between them collapse. Side-by-side or unified, with the context adjustable.
- A second navigation layout. Sections can sit across the top or stacked in a left rail above the project list, the way authentik and Dockhand do it.
- Date and time preferences: 24-hour or 12-hour, day/month/year order, relative or absolute timestamps, and optional seconds. They live in your browser, so two people looking at the same Silt each get their own.
- A Ko-fi link in the version dialog, and a funding entry on the repository.

### Changed

- The project list scrolls on its own instead of making the page taller. A forty-project rail no longer decides how far the timeline scrolls.
- The timeline reads at a glance: a fixed time column, a severity bar instead of a dot, day headings, hairline separators instead of a box per row, and bursts that expand in place. The density strip is taller by default, resizable, has a readable axis, and its legend doubles as a series filter.
- Settings are organised into sections with their own navigation rather than one page taller than the window.
- The strata-and-marker mark is now used throughout — header, favicon, sign-in, empty states.

### Fixed

- Choosing "whole file" when comparing YAML could lock the tab: the context window was walked one step at a time up to the maximum safe integer.

## 0.2.0 — 2026-08-26

Settings you can edit, a timeline you can grab, and somewhere to put the version number.

### Added

- Settings are editable from the UI. The environment stays the baseline; edits are stored as overrides on top of it, and every field shows whether it is coming from the environment or from here.
- Changed settings take effect without a restart: the snapshot interval, retention windows, keep-list, notification targets, base URL, log level and ingest token are all re-read by the running collector.
- The density strip is interactive — hover for the counts in a bucket, drag to zoom into a window, double-click to zoom back out.
- A version button in the header that opens the changelog.
- A Projects page that lists every stack with its last change, for installs where the sidebar is a scroll rather than a glance.

### Changed

- Navigation is a top bar with Timeline, Projects and Settings, plus a project sidebar. Settings no longer sits at the bottom of a thirty-item scroll.
- The theme toggle is an animated sun/moon rather than the word "Light".

### Security

- Notification targets are masked when read back. A shoutrrr URL carries the credential for the service it points at, so the settings screen shows the scheme and, where it is a real host, the host — never the token.

## 0.1.0 — 2026-08-25

First working Silt: it watches, it records, it shows you what changed.

### Added

- Docker event stream and interval reconcile, recording a snapshot of every Compose project whenever its configuration changes.
- Keep-list redaction: every environment value is a keyed digest unless its key is on the safe list, so the history answers "when did this change?" without ever holding the secret.
- Compose file capture with per-line diffs, and manual marking of lines to hide — click a line to redact it, click it again to reveal it.
- Timeline, project, service, diff and settings screens, live-updated over SSE.
- Retention with separate windows for changed snapshots, runtime-only snapshots and events, plus optional vacuuming.
- Notifications through shoutrrr, filtered by change kind and severity.
- Authentication: forward-auth from a reverse proxy, with a bcrypt password fallback.
- Multi-architecture images at ghcr.io/unmaykr-a/silt.
