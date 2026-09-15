# Notifications

Silt sends through [shoutrrr](https://containrrr.dev/shoutrrr/), so any target it supports
works: ntfy, Gotify, Discord, Telegram, Slack, email, generic webhooks.

```yaml
SILT_NOTIFY_URLS: ntfy://ntfy.sh/my-silt-topic
SILT_NOTIFY_ON: image_id,image_digest,volumes,service_removed
SILT_NOTIFY_MIN_SEVERITY: medium
SILT_BASE_URL: https://silt.example.lan
```

## The filter is an AND

`SILT_NOTIFY_ON` lists change kinds. `SILT_NOTIFY_MIN_SEVERITY` sets a floor. A change must
match a listed kind **and** meet the severity to send.

That is deliberate: either dial alone gives you a choice between noise and silence. Together
you get "changes that are both interesting and important".

Kinds include `image_id`, `image_digest`, `image_ref`, `env_added`, `env_removed`,
`env_changed`, `volumes`, `ports`, `labels`, `restart_policy`, `command`, `service_added`,
`service_removed`, `health`, `restart`. `all` matches everything. An unrecognised kind is a
**startup error**, not a silently-never-matching filter — a typo there means the notification
you configured for an outage never arrives, and you find out during the outage.

Severity is `low`, `medium` or `high`. An image digest moving is high; a label is low.

## Set `SILT_BASE_URL`

Without it a notification can say what changed but not where to look at it — in the one
message you needed to be useful. With it, every message links to the diff.

The Setup section of the settings screen reports a missing base URL as a warning when
notifications are configured, for exactly this reason.

## Test before you need it

A shoutrrr URL is wrong until something tries to send, and the only thing that tries to send is
the change that mattered. Without a test, the first proof that notifications work is the outage
they were configured for.

Settings → Notifications → **Send a test** sends to every configured target and reports each
one individually, with the provider's own error where it failed.

It tests **what is saved, not what is typed** in the box above it — so save first.

## Targets are masked

A shoutrrr URL carries the credential for the service it points at. Silt shows the scheme and,
where it is a real host rather than an identifier, the host — never the token. Typing in the box
replaces the whole list; the existing targets are listed above it so you know what you are
replacing.

The settings export names the targets it left out rather than silently dropping them, because a
notification target restored as a blank is a restore that quietly stops notifying.
