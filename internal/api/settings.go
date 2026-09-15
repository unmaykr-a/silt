package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/unmaykr-a/silt/internal/changelog"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/notify"
	"github.com/unmaykr-a/silt/internal/settings"
	"github.com/unmaykr-a/silt/internal/store"
)

// settingsValues is the editable half of the configuration.
//
// Secrets never appear here. The ingest token is reported as set-or-not, and
// notification targets are masked: a shoutrrr URL is the credential for the
// service it points at, so handing the list back to whoever opened the
// settings screen would turn a read of the UI into a read of the secrets.
type settingsValues struct {
	SnapshotIntervalMS     int64    `json:"snapshot_interval_ms"`
	RetentionDays          int      `json:"retention_days"`
	UnchangedRetentionDays int      `json:"unchanged_retention_days"`
	EventRetentionDays     int      `json:"event_retention_days"`
	AuditRetentionDays     int      `json:"audit_retention_days"`
	RetentionIntervalMS    int64    `json:"retention_interval_ms"`
	VacuumIntervalMS       int64    `json:"vacuum_interval_ms"`
	KeepKeys               []string `json:"keep_keys"`
	BaseURL                string   `json:"base_url"`
	LogLevel               string   `json:"log_level"`
	NotifyTargets          []string `json:"notify_targets"`
	NotifyOn               []string `json:"notify_on"`
	NotifyMinSeverity      string   `json:"notify_min_severity"`
	IngestConfigured       bool     `json:"ingest_configured"`
	// IngestRatePerMinute is the per-source cap on webhook events. It is a
	// value rather than a secret, so unlike the token it is readable.
	IngestRatePerMinute int `json:"ingest_rate_per_minute"`
	// HostName labels this Docker host. Editing it renames the existing host
	// row rather than starting a second one, which is what the environment
	// variable alone could never do: the row is keyed on the name, so changing
	// it and restarting used to orphan every project under the old label.
	HostName string `json:"host_name"`
	// DockerHost is the endpoint being observed. Changing it redials without a
	// restart; the event stream ends and reconnects to the new engine.
	DockerHost string `json:"docker_host"`
	// MetricsPublic leaves /metrics reachable without authentication.
	MetricsPublic bool `json:"metrics_public"`
	// MaxComposeFileBytes caps a single captured file.
	MaxComposeFileBytes int64 `json:"max_compose_file_bytes"`

	// Authentication. Editable since 1.2.0, which is a deliberate change of
	// position rather than a gap being closed: these were withheld on the
	// grounds that a UI able to edit the boundary in front of it is a way in.
	//
	// What changed the answer is that only an administrator can reach them, and
	// an administrator already holds the stronger controls — the whole-database
	// backup, the password, every session. Withholding these bought nothing and
	// cost a container recreate to fix a typo'd issuer. SILT_SETTINGS_RESET=1
	// is the way back in if a save goes wrong.
	//
	// The client secret is not here: it is write-only, like the ingest token,
	// and reported through Identity as set-or-not.
	LocalAccount      bool     `json:"local_account"`
	TrustProxyAuth    bool     `json:"trust_proxy_auth"`
	AuthHeader        string   `json:"auth_header"`
	AuthGroupsHeader  string   `json:"auth_groups_header"`
	AdminGroups       []string `json:"admin_groups"`
	TrustedProxies    []string `json:"trusted_proxies"`
	OIDCIssuer        string   `json:"oidc_issuer"`
	OIDCClientID      string   `json:"oidc_client_id"`
	OIDCRedirectURL   string   `json:"oidc_redirect_url"`
	OIDCScopes        []string `json:"oidc_scopes"`
	OIDCUsernameClaim string   `json:"oidc_username_claim"`
	OIDCGroupsClaim   string   `json:"oidc_groups_claim"`
	OIDCAdminGroups   []string `json:"oidc_admin_groups"`
	OIDCAllowedGroups []string `json:"oidc_allowed_groups"`
	OIDCAllowedUsers  []string `json:"oidc_allowed_users"`
	SessionTTLMS      int64    `json:"session_ttl_ms"`
	SessionIdleTTLMS  int64    `json:"session_idle_ttl_ms"`
	OIDCAdminTTLMS    int64    `json:"oidc_admin_ttl_ms"`
	CookieSecure      string   `json:"cookie_secure"`
}

// settingsFixed is the half that stays in the environment for good.
//
// Three settings, and each for a reason the settings screen could not work
// around. The listen address and the database path are consumed once, by a
// socket and a file handle that cannot be rebuilt underneath a running
// process. The compose roots are an allowlist whose entries only mean anything
// alongside a matching read-only volume mount, so a path typed in here would
// name a directory this container cannot see.
//
// Everything else that used to be in here is editable now. What kept most of
// it out was the cost of adding a setting, not a property of the setting.
type settingsFixed struct {
	DBPath       string   `json:"db_path"`
	ListenAddr   string   `json:"listen_addr"`
	ComposeRoots []string `json:"compose_roots"`
	AuthMode     string   `json:"auth_mode"`
}

// settingsIdentity is what can be said about authentication that is not itself
// a setting.
//
// The settings moved to settingsValues in 1.2.0 when they became editable. What
// is left is derived — the mode this adds up to, whether the two credentials are
// set, and whether the admin/viewer split is in play — and none of it has a
// variable behind it or a control to offer.
//
// Secrets are reported as configured-or-not, never echoed, exactly as the
// notification targets and the ingest token are.
type settingsIdentity struct {
	// Mode is the sign-in method this configuration adds up to.
	Mode string `json:"mode"`
	// PasswordHashSet and OIDCSecretSet stand in for two credentials that are
	// never returned. The password hash is not even editable — SILT_PASSWORD_HASH
	// exists to take the password out of the UI's hands for an install managed
	// declaratively, and a UI that could set it would defeat its only purpose.
	PasswordHashSet bool `json:"password_hash_set"`
	OIDCSecretSet   bool `json:"oidc_secret_set"`
	// RolesEnabled is whether anyone is a viewer rather than an administrator.
	// Without an admin group configured everyone admitted may change
	// everything, which is what Silt did before roles existed.
	RolesEnabled bool `json:"roles_enabled"`
}

type settingsUsage struct {
	Blobs             int64 `json:"blobs"`
	StoredBytes       int64 `json:"stored_bytes"`
	UncompressedBytes int64 `json:"uncompressed_bytes"`
	Events            int64 `json:"events"`
}

// settingsResponse is what the settings screen renders.
type settingsResponse struct {
	Version string `json:"version"`
	Release string `json:"release"`
	// Effective is what is in force: the environment with any stored
	// overrides applied.
	Effective settingsValues `json:"effective"`
	// Environment is the baseline, shown beside any overridden value so the
	// screen can say what taking the override back would restore.
	Environment settingsValues `json:"environment"`
	// Overridden names the fields whose value comes from the database.
	Overridden []string `json:"overridden"`
	// Editable is false when there is nowhere to store an override.
	Editable bool             `json:"editable"`
	Fixed    settingsFixed    `json:"fixed"`
	Identity settingsIdentity `json:"identity"`
	Usage    settingsUsage    `json:"usage"`
	// Checks is what is worth knowing about this configuration: the settings
	// that are legal, working, and probably not what was meant. See
	// config.Config.Checks.
	Checks []config.Check `json:"checks"`
}

func toValues(c config.Config) settingsValues {
	v := settingsValues{
		SnapshotIntervalMS:     c.SnapshotInterval.Milliseconds(),
		RetentionDays:          c.RetentionDays,
		UnchangedRetentionDays: c.UnchangedRetentionDays,
		EventRetentionDays:     c.EventRetentionDays,
		AuditRetentionDays:     c.AuditRetentionDays,
		RetentionIntervalMS:    c.RetentionInterval.Milliseconds(),
		VacuumIntervalMS:       c.VacuumInterval.Milliseconds(),
		KeepKeys:               c.KeepKeys,
		BaseURL:                c.BaseURL,
		LogLevel:               c.LogLevel,
		NotifyTargets:          notify.MaskAll(c.NotifyURLs),
		NotifyOn:               c.NotifyOn,
		NotifyMinSeverity:      c.NotifyMinSeverity,
		IngestConfigured:       c.IngestToken != "",
		IngestRatePerMinute:    c.IngestRatePerMinute,
		HostName:               c.HostName,
		DockerHost:             c.DockerHost,
		MetricsPublic:          c.MetricsPublic,
		MaxComposeFileBytes:    c.MaxComposeFileBytes,

		LocalAccount:      c.LocalAccount,
		TrustProxyAuth:    c.TrustProxyAuth,
		AuthHeader:        c.AuthHeader,
		AuthGroupsHeader:  c.AuthGroupsHeader,
		AdminGroups:       orEmpty(c.AdminGroups),
		TrustedProxies:    orEmpty(c.TrustedProxies),
		OIDCIssuer:        c.OIDCIssuer,
		OIDCClientID:      c.OIDCClientID,
		OIDCRedirectURL:   c.OIDCRedirectURL,
		OIDCScopes:        orEmpty(c.OIDCScopes),
		OIDCUsernameClaim: c.OIDCUsernameClaim,
		OIDCGroupsClaim:   c.OIDCGroupsClaim,
		OIDCAdminGroups:   orEmpty(c.OIDCAdminGroups),
		OIDCAllowedGroups: orEmpty(c.OIDCAllowedGroups),
		OIDCAllowedUsers:  orEmpty(c.OIDCAllowedUsers),
		SessionTTLMS:      c.SessionTTL.Milliseconds(),
		SessionIdleTTLMS:  c.SessionIdleTTL.Milliseconds(),
		OIDCAdminTTLMS:    c.OIDCAdminTTL.Milliseconds(),
		CookieSecure:      c.CookieSecure,
	}
	if v.KeepKeys == nil {
		v.KeepKeys = []string{}
	}
	if v.NotifyOn == nil {
		v.NotifyOn = []string{}
	}
	return v
}

// orEmpty keeps an unset list out of the payload as [] rather than null: the
// screen renders "none", and null would render as a gap.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *Server) authMode(c config.Config) string {
	switch {
	case c.TrustProxyAuth && c.PasswordHash != "":
		return "proxy+password"
	case c.TrustProxyAuth:
		return "proxy"
	case c.PasswordHash != "":
		return "password"
	default:
		return "none"
	}
}

func (s *Server) settingsPayload(r *http.Request) settingsResponse {
	effective := s.conf()
	base := effective
	overridden := []string{}
	editable := false
	if s.live != nil {
		base = s.live.Base()
		overridden = s.live.Overrides().Names()
		editable = true
	}

	out := settingsResponse{
		Version:     s.version,
		Release:     changelog.Current(),
		Effective:   toValues(effective),
		Environment: toValues(base),
		Overridden:  overridden,
		Editable:    editable,
		Fixed: settingsFixed{
			DBPath:       effective.DBPath,
			ListenAddr:   effective.ListenAddr,
			ComposeRoots: effective.ComposeRoots,
			AuthMode:     s.authMode(effective),
		},
	}
	if out.Fixed.ComposeRoots == nil {
		out.Fixed.ComposeRoots = []string{}
	}

	out.Identity = settingsIdentity{
		Mode:            s.authMode(effective),
		PasswordHashSet: effective.PasswordHash != "",
		OIDCSecretSet:   effective.OIDCClientSecret != "",
		RolesEnabled:    len(effective.OIDCAdminGroups) > 0 || len(effective.AdminGroups) > 0,
	}
	out.Checks = effective.Checks()
	if out.Checks == nil {
		out.Checks = []config.Check{}
	}

	if usage, err := s.store.Usage(r.Context()); err == nil {
		out.Usage.Blobs = usage.Blobs
		out.Usage.StoredBytes = usage.StoredBytes
		out.Usage.UncompressedBytes = usage.UncompressedBytes
	}
	if events, err := s.store.RQ.CountEvents(r.Context()); err == nil {
		out.Usage.Events = events
	}
	return out
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsPayload(r))
}

// settingsExport is the override document, portable.
//
// Silt stores settings as a sparse patch on top of the environment, so this is
// already the shape of "what has been changed here" — the export is that
// document with a header saying where it came from, not a new format.
//
// Secrets are stripped, and the export says which were dropped rather than
// leaving the reader to notice. A file that silently omits your notification
// targets is a restore that silently stops notifying.
type settingsExport struct {
	Silt       string             `json:"silt"`
	Release    string             `json:"release"`
	HostName   string             `json:"host_name"`
	ExportedAt int64              `json:"exported_at"`
	Settings   settings.Overrides `json:"settings"`
	// Omitted names the secret fields that were set and left out.
	Omitted []string `json:"omitted,omitempty"`
	Note    string   `json:"note"`
}

// filenameSafe reduces a host name to something that can sit inside a
// Content-Disposition filename.
//
// SILT_HOST_NAME is a free-text label, so it can hold a quote, a slash or a
// newline — and a header value is none of those things. Nothing here is an
// attack, since the value is the operator's own, but naming your host
// `my "prod" box` should not break the download.
func filenameSafe(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "host"
	}
	// Long enough to be recognisable, short enough for any filesystem.
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func (s *Server) exportSettings(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "settings are read-only in this configuration")
		return
	}
	cfg := s.conf()
	out := settingsExport{
		Silt:       "settings",
		Release:    changelog.Current(),
		HostName:   cfg.HostName,
		ExportedAt: nowMS(),
		Settings:   s.live.Overrides(),
		Note: "Overrides only: anything not listed here comes from the environment. " +
			"Import with PUT /api/settings, or paste into the settings screen.",
	}

	// A shoutrrr URL carries the credential for the service it points at, and
	// the ingest token is a credential outright. Neither is readable anywhere
	// else in the API and neither becomes readable by being called an export.
	out.Settings, out.Omitted = out.Settings.WithoutSecrets()

	// Downloaded rather than rendered: this is a file someone keeps.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "silt-settings-"+filenameSafe(cfg.HostName)+".json"))
	writeJSON(w, http.StatusOK, out)
}

// reset names the settings a patch is giving back to the environment. It
// travels in the same object as the values, so it is lifted out before the rest
// is read as a set of overrides.
const resetKey = "reset"

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "settings are read-only in this configuration")
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	var reset []string
	if raw, ok := body[resetKey]; ok {
		if err := json.Unmarshal(raw, &reset); err != nil {
			writeError(w, http.StatusBadRequest, "reset must be a list of setting names")
			return
		}
		delete(body, resetKey)
	}
	// An unknown name is refused here. It used to decode into a struct, and
	// encoding/json drops what it does not recognise, so a patch naming
	// retention_dayz answered 200 and changed nothing.
	next, err := settings.NewOverrides(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The notification filter is validated here rather than in config, which
	// has no business knowing what a change kind is. Rejecting it before the
	// write means a typo'd severity never reaches the collector. Checked
	// against what the patch would produce rather than against the patch, so
	// changing one half of the pair is validated with the half already stored.
	prospective, err := s.live.Preview(next, reset)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := notify.ParseFilter(prospective.NotifyOn, prospective.NotifyMinSeverity); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := s.live.Update(r.Context(), next, reset); err != nil {
		if errors.Is(err, settings.ErrReadOnly) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fields := next.Names()
	s.log.Info("settings updated", "fields", fields, "reset", reset)
	// Field names, never values. This table holds an ingest token and
	// notification URLs; recording what they became would put credentials in
	// a table built to be read.
	s.audit(r, store.AuditSettingsChanged, map[string]any{"fields": fields, "reset": reset})
	writeJSON(w, http.StatusOK, s.settingsPayload(r))
}

func (s *Server) deleteSettings(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "settings are read-only in this configuration")
		return
	}
	if _, err := s.live.Reset(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "reset settings")
		return
	}
	s.log.Info("settings reset to the environment")
	s.audit(r, store.AuditSettingsReset, nil)
	writeJSON(w, http.StatusOK, s.settingsPayload(r))
}

type pruneResponse struct {
	UnchangedSnapshots int64 `json:"unchanged_snapshots"`
	ChangedSnapshots   int64 `json:"changed_snapshots"`
	Events             int64 `json:"events"`
	Blobs              int64 `json:"blobs"`
}

// postPrune runs a retention pass now, using the configured policy rather than
// letting a caller choose one: a request that could pass its own retention
// window would be a delete endpoint wearing a different name.
func (s *Server) postPrune(w http.ResponseWriter, r *http.Request) {
	cfg := s.conf()
	policy := store.RetentionPolicy{
		Changed:   config.Days(cfg.RetentionDays),
		Unchanged: config.Days(cfg.UnchangedRetentionDays),
		Events:    config.Days(cfg.EventRetentionDays),
		Audit:     config.Days(cfg.AuditRetentionDays),
	}
	stats, err := s.store.Prune(r.Context(), policy, time.Now())
	if err != nil {
		s.log.Error("manual prune failed", "error", err)
		writeError(w, http.StatusInternalServerError, "prune failed")
		return
	}
	// A prune deletes history permanently, which makes it the one maintenance
	// action worth being able to attribute afterwards.
	s.audit(r, store.AuditPrune, map[string]any{
		"unchanged_snapshots": stats.UnchangedSnapshots,
		"changed_snapshots":   stats.ChangedSnapshots,
		"events":              stats.Events,
		"blobs":               stats.Blobs,
	})
	writeJSON(w, http.StatusOK, pruneResponse{
		UnchangedSnapshots: stats.UnchangedSnapshots,
		ChangedSnapshots:   stats.ChangedSnapshots,
		Events:             stats.Events,
		Blobs:              stats.Blobs,
	})
}

// versionResponse backs the version button in the header.
type versionResponse struct {
	// Version is the build stamp: a tag on a release, a commit otherwise.
	Version string `json:"version"`
	// Release is the newest entry in the changelog, which is what the notes
	// below it describe.
	Release  string              `json:"release"`
	Releases []changelog.Release `json:"releases"`
}

func (s *Server) getVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{
		Version:  s.version,
		Release:  changelog.Current(),
		Releases: changelog.Releases,
	})
}

// testNotifications sends one message to each configured target.
//
// A shoutrrr URL is fire-and-forget: it is wrong until something tries to send,
// and the only thing that tries to send is the change that mattered. This makes
// the failure discoverable at the moment someone configures it.
//
// It reaches out to hosts named in the configuration, so it needs a session —
// but it adds no capability a signed-in operator did not already have. The
// targets are the ones already in the settings; nothing in the request chooses
// where the message goes, which is what keeps it from being a way to make Silt
// fetch arbitrary URLs.
func (s *Server) testNotifications(w http.ResponseWriter, r *http.Request) {
	urls := s.conf().NotifyURLs
	if len(urls) == 0 {
		writeJSON(w, http.StatusOK, notifyTestResponse{Results: []notifyTestResult{}})
		return
	}

	// One target can take up to notify.TestTimeout, and they run in order, so
	// the request deadline has to allow for all of them.
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(len(urls))*notify.TestTimeout+5*time.Second)
	defer cancel()

	s.audit(r, store.AuditNotifyTested, map[string]any{"targets": len(urls)})

	out := notifyTestResponse{Results: []notifyTestResult{}}
	for _, result := range notify.Test(ctx, urls) {
		if !result.OK {
			out.Failed++
		}
		out.Results = append(out.Results, notifyTestResult{
			Index:  result.Index,
			Target: result.Target,
			OK:     result.OK,
			Error:  result.Error,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type notifyTestResult struct {
	Index  int    `json:"index"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	// Error is masked: a provider's message routinely quotes the request URL,
	// and a shoutrrr URL is a credential.
	Error string `json:"error,omitempty"`
}

type notifyTestResponse struct {
	Results []notifyTestResult `json:"results"`
	Failed  int                `json:"failed"`
}
