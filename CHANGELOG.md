# Changelog

All notable changes to Silt are recorded here.

This file is generated from internal/changelog/changelog.go — edit that and run
`make changelog`.

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
