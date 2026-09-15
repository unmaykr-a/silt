# Webhook ingest

External events share the timeline with your configuration changes. An Uptime Kuma probe, a
cron job, a Home Assistant automation, a backup script — anything that can `POST` JSON.

This is the other half of the point of the app: a change and the outage it might explain are
only useful side by side.

```yaml
SILT_INGEST_TOKEN: a-long-random-string
SILT_INGEST_RATE_PER_MINUTE: 60
```

**Unset the token and the endpoint returns 503.** Unset never means open.

## Sending

```bash
curl -X POST https://silt.example.lan/api/ingest \
  -H "Authorization: Bearer $SILT_INGEST_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "type": "probe.down",
        "project": "media",
        "service": "radarr",
        "severity": "high",
        "message": "HTTP check failed: 502 Bad Gateway",
        "actor": "uptime-kuma"
      }'
```

The token also works as `?token=...`, because not every webhook source can set custom headers,
and a webhook nobody can call is not a feature.

| Field | | |
|---|---|---|
| `type` | **required** | Free-form, e.g. `probe.down`, `backup.finished`. |
| `project` | optional | Matched against known project names. |
| `service` | optional | Used to find the project if `project` is absent — monitors are usually named after a service. |
| `severity` | optional | `low`, `medium`, `high`. Defaults to medium. |
| `message` | optional | Shown on the timeline. |
| `actor` | optional | Who sent it. |
| `ts` | optional | Unix milliseconds. Defaults to now. |

An event that matches no project is still recorded, at host level, rather than dropped.

Responds `202` with the new event's id.

## Uptime Kuma

Add a **Webhook** notification, set the URL to
`https://silt.example.lan/api/ingest?token=YOUR_TOKEN`, content type JSON, and use a custom
body:

```json
{
  "type": "probe.{{ heartbeat.status == 1 ? \"up\" : \"down\" }}",
  "service": "{{ monitor.name }}",
  "severity": "{{ heartbeat.status == 1 ? \"low\" : \"high\" }}",
  "message": "{{ heartbeat.msg }}",
  "actor": "uptime-kuma"
}
```

## Limits

- **Rate limited per source address**, `SILT_INGEST_RATE_PER_MINUTE` (default 60). Over it,
  Silt answers `429` with a `Retry-After`.
- The limit is checked **after** the token, so an unauthenticated caller cannot spend a real
  sender's allowance — that would make the limiter a way to silence your monitoring rather than
  a way to protect it.
- Body capped at 64 KiB.
- The token is compared in constant time.

## Do not send secrets

Event messages are stored **as received**. Redaction applies to configuration Silt reads, not
to prose you hand it. If you `POST` a secret in a message, Silt stores it.
