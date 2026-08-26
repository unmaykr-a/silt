// Package config loads Silt's configuration from the environment.
//
// Silt is configured entirely by environment variables, as self-hosters
// expect. Every knob is documented in PROJECT.md Section 13; this struct
// carries the subset that the current milestone actually reads, and grows as
// features land rather than declaring options that do nothing.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the fully validated runtime configuration.
type Config struct {
	// ListenAddr is the address the HTTP server binds to.
	ListenAddr string `env:"SILT_LISTEN_ADDR" envDefault:":8375"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `env:"SILT_LOG_LEVEL" envDefault:"info"`
	// DockerHost is the Docker API endpoint. The documented default is a
	// read-only socket proxy, never the socket itself: mounting
	// /var/run/docker.sock:ro is not a security boundary, because read-only
	// applies to the file and not to the API. See PROJECT.md Section 3.
	DockerHost string `env:"SILT_DOCKER_HOST" envDefault:"tcp://docker-socket-proxy:2375"`

	// DBPath is the SQLite file.
	DBPath string `env:"SILT_DB_PATH" envDefault:"/data/silt.db"`
	// SnapshotInterval is the reconcile cadence that catches anything the
	// event stream missed.
	SnapshotInterval time.Duration `env:"SILT_SNAPSHOT_INTERVAL" envDefault:"5m"`

	// RetentionDays covers snapshots whose configuration changed.
	RetentionDays int `env:"SILT_RETENTION_DAYS" envDefault:"365"`
	// UnchangedRetentionDays covers proof-of-liveness snapshots.
	UnchangedRetentionDays int `env:"SILT_UNCHANGED_RETENTION_DAYS" envDefault:"7"`
	// EventRetentionDays is separate because event volume exceeds snapshot
	// volume by orders of magnitude.
	EventRetentionDays int `env:"SILT_EVENT_RETENTION_DAYS" envDefault:"90"`
	// VacuumInterval of 0 disables vacuuming.
	VacuumInterval time.Duration `env:"SILT_VACUUM_INTERVAL" envDefault:"0"`
	// RetentionInterval is how often the retention pass runs.
	RetentionInterval time.Duration `env:"SILT_RETENTION_INTERVAL" envDefault:"1h"`

	// KeepKeys extends the built-in list of environment keys kept in
	// cleartext. There is no redact-list: everything else is redacted.
	KeepKeys []string `env:"SILT_KEEP_KEYS" envSeparator:","`

	// HostName labels this Docker host in the database.
	HostName string `env:"SILT_HOST_NAME" envDefault:"local"`

	// IngestToken guards POST /api/ingest. Empty means the endpoint is not
	// configured and returns 503 — unset must never mean open.
	IngestToken string `env:"SILT_INGEST_TOKEN"`

	// NotifyURLs are shoutrrr targets. Empty disables notifications.
	NotifyURLs []string `env:"SILT_NOTIFY_URLS" envSeparator:","`
	// NotifyOn lists the change kinds worth interrupting someone for.
	NotifyOn []string `env:"SILT_NOTIFY_ON" envSeparator:"," envDefault:"image_id,image_digest,volumes,service_removed"`
	// NotifyMinSeverity is ANDed with NotifyOn.
	NotifyMinSeverity string `env:"SILT_NOTIFY_MIN_SEVERITY" envDefault:"medium"`
	// BaseURL is used to build links in notifications. Empty omits the link.
	BaseURL string `env:"SILT_BASE_URL"`

	// TrustProxyAuth accepts an identity asserted by a reverse proxy.
	TrustProxyAuth bool `env:"SILT_TRUST_PROXY_AUTH" envDefault:"false"`
	// AuthHeader is the forward-auth header name.
	AuthHeader string `env:"SILT_AUTH_HEADER" envDefault:"X-Remote-User"`
	// TrustedProxies are the addresses or CIDR ranges whose forward-auth
	// header is believed.
	//
	// Without this the header is believed from any source, and since anyone
	// who can open a socket can set a header, "authenticated" would mean
	// "reached the port" — which on a shared Docker network is every other
	// container on it.
	TrustedProxies []string `env:"SILT_TRUSTED_PROXIES" envSeparator:","`
	// PasswordHash is a bcrypt hash for the fallback login.
	PasswordHash string `env:"SILT_PASSWORD_HASH"`

	// OIDCIssuer enables OpenID Connect login. Empty disables it.
	OIDCIssuer string `env:"SILT_OIDC_ISSUER"`
	// OIDCClientID and OIDCClientSecret are the registered client.
	OIDCClientID     string `env:"SILT_OIDC_CLIENT_ID"`
	OIDCClientSecret string `env:"SILT_OIDC_CLIENT_SECRET"`
	// OIDCRedirectURL must match the provider's registration exactly. Empty
	// derives it from BaseURL.
	OIDCRedirectURL string `env:"SILT_OIDC_REDIRECT_URL"`
	// OIDCScopes always includes openid, whether or not it is listed.
	OIDCScopes []string `env:"SILT_OIDC_SCOPES" envSeparator:"," envDefault:"openid,profile,email"`
	// OIDCUsernameClaim and OIDCGroupsClaim differ between providers.
	OIDCUsernameClaim string `env:"SILT_OIDC_USERNAME_CLAIM" envDefault:"preferred_username"`
	OIDCGroupsClaim   string `env:"SILT_OIDC_GROUPS_CLAIM" envDefault:"groups"`
	// OIDCAllowedGroups and OIDCAllowedUsers restrict who may sign in. Both
	// empty admits anyone the provider authenticates.
	OIDCAllowedGroups []string `env:"SILT_OIDC_ALLOWED_GROUPS" envSeparator:","`
	OIDCAllowedUsers  []string `env:"SILT_OIDC_ALLOWED_USERS" envSeparator:","`

	// SessionTTL is how long a session lasts regardless of activity.
	SessionTTL time.Duration `env:"SILT_SESSION_TTL" envDefault:"720h"`
	// SessionIdleTTL ends an unused session early. Zero disables it.
	SessionIdleTTL time.Duration `env:"SILT_SESSION_IDLE_TTL" envDefault:"168h"`

	// MetricsPublic leaves /metrics reachable without authentication.
	//
	// It is off by default now: the endpoint names every project on the host
	// and counts its changes, which is not something to hand to anyone who can
	// reach the port just because Prometheus finds a token inconvenient.
	MetricsPublic bool `env:"SILT_METRICS_PUBLIC" envDefault:"false"`

	// ComposeRoots are host paths, mounted read-only into Silt, under which
	// compose files may be read.
	//
	// This is an allowlist, not a hint. The paths Silt would otherwise follow
	// come from container labels, and anyone who can start a container can set
	// those, so without a root check a crafted label could point Silt at any
	// file it can reach.
	ComposeRoots []string `env:"SILT_COMPOSE_ROOTS" envSeparator:","`
	// MaxComposeFileBytes caps a single captured file.
	MaxComposeFileBytes int64 `env:"SILT_MAX_COMPOSE_FILE_BYTES" envDefault:"1048576"`
}

// Load reads the environment, applies defaults, and validates the result.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate checks the configuration and normalises the fields that have a
// canonical form. It is exported because the settings layer applies database
// overrides on top of the environment and must re-run the same checks: a value
// typed into the UI has to clear exactly the bar an environment variable does.
func (c *Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("SILT_LISTEN_ADDR %q is not a valid host:port: %w", c.ListenAddr, err)
	}
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	if _, err := parseLevel(c.LogLevel); err != nil {
		return err
	}
	if err := validateDockerHost(c.DockerHost); err != nil {
		return err
	}
	if c.DBPath == "" {
		return fmt.Errorf("SILT_DB_PATH must not be empty")
	}
	if c.SnapshotInterval < time.Second {
		return fmt.Errorf("SILT_SNAPSHOT_INTERVAL %v is too short; use at least 1s", c.SnapshotInterval)
	}
	if c.RetentionInterval < time.Minute {
		return fmt.Errorf("SILT_RETENTION_INTERVAL %v is too short; use at least 1m", c.RetentionInterval)
	}
	for name, days := range map[string]int{
		"SILT_RETENTION_DAYS":           c.RetentionDays,
		"SILT_UNCHANGED_RETENTION_DAYS": c.UnchangedRetentionDays,
		"SILT_EVENT_RETENTION_DAYS":     c.EventRetentionDays,
	} {
		if days < 0 {
			return fmt.Errorf("%s must not be negative, got %d", name, days)
		}
	}
	for i, root := range c.ComposeRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("SILT_COMPOSE_ROOTS entry %q must be an absolute path", root)
		}
		c.ComposeRoots[i] = filepath.Clean(root)
	}
	if c.MaxComposeFileBytes <= 0 {
		return fmt.Errorf("SILT_MAX_COMPOSE_FILE_BYTES must be positive, got %d", c.MaxComposeFileBytes)
	}
	if c.SessionTTL < time.Minute {
		return fmt.Errorf("SILT_SESSION_TTL %v is too short; use at least 1m", c.SessionTTL)
	}
	if c.SessionIdleTTL < 0 {
		return fmt.Errorf("SILT_SESSION_IDLE_TTL must not be negative, got %v", c.SessionIdleTTL)
	}
	if c.OIDCIssuer != "" {
		u, err := url.Parse(c.OIDCIssuer)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("SILT_OIDC_ISSUER %q must be an absolute URL, e.g. https://auth.example.com/application/o/silt/", c.OIDCIssuer)
		}
		// A provider reached over plain HTTP would carry the authorization
		// code and the id_token in clear text. Loopback is exempt because a
		// developer running a provider locally has no other option.
		if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("SILT_OIDC_ISSUER %q must use https", c.OIDCIssuer)
		}
		if c.OIDCClientID == "" {
			return fmt.Errorf("SILT_OIDC_ISSUER is set, so SILT_OIDC_CLIENT_ID is required")
		}
		if c.OIDCRedirectURL == "" && c.BaseURL == "" {
			return fmt.Errorf("SILT_OIDC_ISSUER is set, so either SILT_OIDC_REDIRECT_URL or SILT_BASE_URL is required to build the callback URL")
		}
	}
	if c.UnchangedRetentionDays > c.RetentionDays && c.RetentionDays > 0 {
		return fmt.Errorf(
			"SILT_UNCHANGED_RETENTION_DAYS (%d) exceeds SILT_RETENTION_DAYS (%d): unchanged snapshots would outlive the changes they sit between",
			c.UnchangedRetentionDays, c.RetentionDays)
	}
	return nil
}

// Clone returns a deep copy. The slice fields matter: the settings layer
// builds an effective configuration by copying the environment baseline and
// overwriting parts of it, and a shared backing array would let one edit reach
// back into the baseline.
func (c Config) Clone() Config {
	out := c
	out.KeepKeys = append([]string(nil), c.KeepKeys...)
	out.NotifyURLs = append([]string(nil), c.NotifyURLs...)
	out.NotifyOn = append([]string(nil), c.NotifyOn...)
	out.ComposeRoots = append([]string(nil), c.ComposeRoots...)
	out.TrustedProxies = append([]string(nil), c.TrustedProxies...)
	out.OIDCScopes = append([]string(nil), c.OIDCScopes...)
	out.OIDCAllowedGroups = append([]string(nil), c.OIDCAllowedGroups...)
	out.OIDCAllowedUsers = append([]string(nil), c.OIDCAllowedUsers...)
	return out
}

// OIDCCallbackURL is where the provider sends the browser back to.
func (c Config) OIDCCallbackURL() string {
	if c.OIDCRedirectURL != "" {
		return c.OIDCRedirectURL
	}
	if c.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(c.BaseURL, "/") + "/api/auth/callback"
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Days converts a retention setting to a duration. Zero means keep forever.
func Days(d int) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(d) * 24 * time.Hour
}

func validateDockerHost(host string) error {
	u, err := url.Parse(host)
	if err != nil {
		return fmt.Errorf("SILT_DOCKER_HOST %q is not a valid URL: %w", host, err)
	}
	switch u.Scheme {
	case "tcp", "http", "https", "unix":
		return nil
	case "":
		return fmt.Errorf("SILT_DOCKER_HOST %q needs a scheme, e.g. tcp://host:2375", host)
	default:
		return fmt.Errorf("SILT_DOCKER_HOST %q has unsupported scheme %q; want tcp, http, https or unix", host, u.Scheme)
	}
}

// Level returns the parsed slog level. Load guarantees this cannot fail.
func (c Config) Level() slog.Level {
	lvl, err := parseLevel(c.LogLevel)
	if err != nil {
		return slog.LevelInfo
	}
	return lvl
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("SILT_LOG_LEVEL %q is not one of debug, info, warn, error", s)
	}
}
