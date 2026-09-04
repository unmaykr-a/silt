package config

import (
	"fmt"
	"strings"
)

// Setup checks: the answer to "is this install actually configured?"
//
// Silt's configuration is thirty-odd environment variables, most of which do
// something sensible when unset. That is the right default and it hides a
// specific failure: a setting that is *almost* right produces no error at
// startup and no symptom until the day it matters. Forward auth trusted with
// no proxy list. Notifications configured with no base URL, so every message
// links nowhere. Compose roots set but never mounted.
//
// Each of those has bitten someone, and each was discoverable only by reading
// the whole environment and knowing what to look for. This is that reading,
// done once, in a list.
//
// Deliberately not validation: Config.Validate refuses to start on anything
// that is wrong. These are the things that are *legal* and probably not what
// you meant, which is why they are advice and not an error.

// Level is how much attention a check wants.
type Level string

const (
	// LevelError is a setting that cannot do its job as configured.
	LevelError Level = "error"
	// LevelWarn is legal, working, and probably not what was intended.
	LevelWarn Level = "warn"
	// LevelInfo is worth stating once so nobody has to go looking.
	LevelInfo Level = "info"
)

// Check is one finding about the configuration.
type Check struct {
	// ID is stable, so the UI can key on it and a test can name it.
	ID    string `json:"id"`
	Level Level  `json:"level"`
	// Title is the finding. Detail is what to do about it.
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// EnvVars are the variables involved, so the reader knows where to go.
	EnvVars []string `json:"env_vars,omitempty"`
}

// Checks reports what is worth knowing about this configuration.
//
// Ordered by level, then by the order they are written here, which runs
// roughly from "someone can read your Docker host" down to housekeeping.
func (c Config) Checks() []Check {
	var out []Check
	add := func(level Level, id, title, detail string, envVars ...string) {
		out = append(out, Check{ID: id, Level: level, Title: title, Detail: detail, EnvVars: envVars})
	}

	// Authentication first: it is the only one where being wrong means
	// someone else is reading your Docker host's history.
	authed := c.TrustProxyAuth || c.OIDCIssuer != "" || c.PasswordHash != "" || c.LocalAccount
	if !authed {
		add(LevelError, "auth.none",
			"No authentication is configured",
			"Anyone who can reach this address has full read access to every project, "+
				"file and event Silt has recorded. Enable the built-in account, an "+
				"identity provider, or forward auth from your reverse proxy.",
			"SILT_LOCAL_ACCOUNT", "SILT_OIDC_ISSUER", "SILT_TRUST_PROXY_AUTH")
	}

	// Forward auth without a trust list is the sharpest edge in the whole
	// configuration: the header is settable by anything that can open a
	// socket, so on a shared Docker network "authenticated" means "is a
	// container".
	if c.TrustProxyAuth && len(c.TrustedProxies) == 0 {
		add(LevelError, "auth.untrusted-proxy",
			"Forward auth trusts every client",
			"SILT_TRUST_PROXY_AUTH is on with no trusted proxy list, so the identity "+
				"header is believed from any address that can reach Silt — on a shared "+
				"Docker network, that is every other container on it. Set the proxy's "+
				"address or subnet.",
			"SILT_TRUSTED_PROXIES", "SILT_TRUST_PROXY_AUTH")
	}

	if c.OIDCIssuer != "" && len(c.OIDCAllowedGroups) == 0 && len(c.OIDCAllowedUsers) == 0 {
		add(LevelWarn, "auth.oidc-open",
			"Any account your provider authenticates can sign in",
			"No group or user allowlist is set, so everyone your identity provider "+
				"will authenticate has access. That is right for a provider with only "+
				"your accounts on it, and wrong for a shared one.",
			"SILT_OIDC_ALLOWED_GROUPS", "SILT_OIDC_ALLOWED_USERS")
	}

	if c.MetricsPublic {
		add(LevelWarn, "metrics.public",
			"Metrics are readable without signing in",
			"/metrics is reachable unauthenticated. It carries counts and names, not "+
				"values, but project names are still information about your host.",
			"SILT_METRICS_PUBLIC")
	}

	// Compose capture: configured but not mounted is the common shape, and it
	// looks identical to "not configured" on every screen.
	if len(c.ComposeRoots) == 0 {
		add(LevelInfo, "compose.off",
			"Compose file capture is off",
			"Silt is recording the running configuration but not the files behind it, "+
				"so it cannot show which line changed or notice an edit you never "+
				"applied. Mount your compose directories read-only and list them here.",
			"SILT_COMPOSE_ROOTS")
	}

	// Notifications: the whole point is the message that arrives during an
	// outage, and there is no feedback loop until then.
	switch {
	case len(c.NotifyURLs) == 0:
		add(LevelInfo, "notify.off",
			"No notification targets",
			"Changes are recorded but nothing is sent anywhere. Silt supports any "+
				"shoutrrr target — ntfy, Gotify, Discord, Telegram, email.",
			"SILT_NOTIFY_URLS")
	case c.BaseURL == "":
		add(LevelWarn, "notify.no-base-url",
			"Notifications will not link back to the change",
			"Without a public URL, a notification can say what changed but not where "+
				"to look at it — in the one message you needed to be useful.",
			"SILT_BASE_URL")
	}

	// Retention that keeps everything forever is a legitimate choice and a
	// surprising default to arrive at by accident.
	if c.RetentionDays == 0 && c.UnchangedRetentionDays == 0 && c.EventRetentionDays == 0 {
		add(LevelWarn, "retention.forever",
			"Nothing is ever deleted",
			"Every retention window is set to keep forever. Storage is deduplicated "+
				"and an unchanged observation costs nothing, but events are not "+
				"deduplicated and a busy host produces a great many of them.",
			"SILT_RETENTION_DAYS", "SILT_UNCHANGED_RETENTION_DAYS", "SILT_EVENT_RETENTION_DAYS")
	}

	// A runtime-only window longer than the changed-snapshot window inverts
	// the intent: proof-of-liveness rows outliving the changes they sit
	// between.
	if c.RetentionDays > 0 && c.UnchangedRetentionDays > c.RetentionDays {
		add(LevelWarn, "retention.inverted",
			"Runtime-only snapshots outlive the changes they sit between",
			fmt.Sprintf("Runtime-only snapshots are kept for %d days but changed ones for "+
				"only %d. The proof-of-liveness rows are the cheap, disposable half; "+
				"keeping them longer is almost certainly backwards.",
				c.UnchangedRetentionDays, c.RetentionDays),
			"SILT_UNCHANGED_RETENTION_DAYS", "SILT_RETENTION_DAYS")
	}

	if c.IngestToken == "" {
		add(LevelInfo, "ingest.off",
			"The ingest webhook is disabled",
			"External events — an Uptime Kuma probe, a cron job, a Home Assistant "+
				"automation — can share the timeline with your config changes. Set a "+
				"token to enable the endpoint; unset, it refuses everything.",
			"SILT_INGEST_TOKEN")
	}

	// Keep keys are the one redaction control, and a long list is worth
	// seeing spelled out rather than trusted to memory.
	if len(c.KeepKeys) > 0 {
		add(LevelInfo, "redact.keep-keys",
			fmt.Sprintf("%d extra environment key%s kept readable",
				len(c.KeepKeys), plural(len(c.KeepKeys))),
			"Values matching "+strings.Join(c.KeepKeys, ", ")+" are stored in cleartext "+
				"in addition to the built-in safe list. Everything else is a keyed digest.",
			"SILT_KEEP_KEYS")
	}

	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
