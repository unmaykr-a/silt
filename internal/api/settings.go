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
}

// settingsFixed is the half that cannot be edited here.
//
// Two different reasons, deliberately not separated in the payload because the
// screen says the same thing about both: some of it cannot change without a
// restart (where the process listens, which database it opens), and some of it
// is the boundary protecting this very screen. Letting the UI edit the compose
// root allowlist or the password hash would mean anyone who reached the UI
// could widen what Silt reads or lock out the person who set it up.
type settingsFixed struct {
	HostName            string   `json:"host_name"`
	DockerHost          string   `json:"docker_host"`
	DBPath              string   `json:"db_path"`
	ListenAddr          string   `json:"listen_addr"`
	ComposeRoots        []string `json:"compose_roots"`
	MaxComposeFileBytes int64    `json:"max_compose_file_bytes"`
	AuthMode            string   `json:"auth_mode"`
}

// settingsIdentity is how this install decides who you are.
//
// Read-only, like the rest of settingsFixed and for the sharper of its two
// reasons: these are the boundary protecting this screen, so a UI that could
// edit them would be a way in rather than a setting.
//
// Shown at all because twelve environment variables were readable nowhere.
// When forward auth is not working, or the provider is rejecting everyone, the
// first question is what Silt thinks it was told — and the only way to answer
// it was to go and read the compose file on the host.
//
// Secrets are reported as configured-or-not, never echoed, exactly as the
// notification targets and the ingest token already are.
type settingsIdentity struct {
	Mode              string   `json:"mode"`
	LocalAccount      bool     `json:"local_account"`
	PasswordHashSet   bool     `json:"password_hash_set"`
	TrustProxyAuth    bool     `json:"trust_proxy_auth"`
	AuthHeader        string   `json:"auth_header"`
	TrustedProxies    []string `json:"trusted_proxies"`
	OIDCIssuer        string   `json:"oidc_issuer"`
	OIDCClientID      string   `json:"oidc_client_id"`
	OIDCSecretSet     bool     `json:"oidc_secret_set"`
	OIDCRedirectURL   string   `json:"oidc_redirect_url"`
	OIDCScopes        []string `json:"oidc_scopes"`
	OIDCUsernameClaim string   `json:"oidc_username_claim"`
	OIDCGroupsClaim   string   `json:"oidc_groups_claim"`
	OIDCAllowedGroups []string `json:"oidc_allowed_groups"`
	OIDCAllowedUsers  []string `json:"oidc_allowed_users"`
	OIDCAdminGroups   []string `json:"oidc_admin_groups"`
	AdminGroups       []string `json:"admin_groups"`
	AuthGroupsHeader  string   `json:"auth_groups_header"`
	// RolesEnabled is whether anyone is a viewer rather than an administrator.
	// Without an admin group configured everyone admitted may change
	// everything, which is what Silt did before roles existed.
	RolesEnabled     bool  `json:"roles_enabled"`
	SessionTTLMS     int64 `json:"session_ttl_ms"`
	SessionIdleTTLMS int64 `json:"session_idle_ttl_ms"`
	// OIDCAdminTTLMS bounds how long a provider-granted administrator role
	// survives without a fresh sign-in. 0 means it does not lapse.
	OIDCAdminTTLMS int64  `json:"oidc_admin_ttl_ms"`
	CookieSecure   string `json:"cookie_secure"`
	MetricsPublic  bool   `json:"metrics_public"`
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
		set := s.live.Overrides().Set()
		for _, name := range settings.Fields {
			if set[name] {
				overridden = append(overridden, name)
			}
		}
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
			HostName:            effective.HostName,
			DockerHost:          effective.DockerHost,
			DBPath:              effective.DBPath,
			ListenAddr:          effective.ListenAddr,
			ComposeRoots:        effective.ComposeRoots,
			MaxComposeFileBytes: effective.MaxComposeFileBytes,
			AuthMode:            s.authMode(effective),
		},
	}
	if out.Fixed.ComposeRoots == nil {
		out.Fixed.ComposeRoots = []string{}
	}

	out.Identity = settingsIdentity{
		Mode:              s.authMode(effective),
		LocalAccount:      effective.LocalAccount,
		PasswordHashSet:   effective.PasswordHash != "",
		TrustProxyAuth:    effective.TrustProxyAuth,
		AuthHeader:        effective.AuthHeader,
		TrustedProxies:    orEmpty(effective.TrustedProxies),
		OIDCIssuer:        effective.OIDCIssuer,
		OIDCClientID:      effective.OIDCClientID,
		OIDCSecretSet:     effective.OIDCClientSecret != "",
		OIDCRedirectURL:   effective.OIDCRedirectURL,
		OIDCScopes:        orEmpty(effective.OIDCScopes),
		OIDCUsernameClaim: effective.OIDCUsernameClaim,
		OIDCGroupsClaim:   effective.OIDCGroupsClaim,
		OIDCAllowedGroups: orEmpty(effective.OIDCAllowedGroups),
		OIDCAllowedUsers:  orEmpty(effective.OIDCAllowedUsers),
		OIDCAdminGroups:   orEmpty(effective.OIDCAdminGroups),
		AdminGroups:       orEmpty(effective.AdminGroups),
		AuthGroupsHeader:  effective.AuthGroupsHeader,
		RolesEnabled:      len(effective.OIDCAdminGroups) > 0 || len(effective.AdminGroups) > 0,
		SessionTTLMS:      effective.SessionTTL.Milliseconds(),
		SessionIdleTTLMS:  effective.SessionIdleTTL.Milliseconds(),
		OIDCAdminTTLMS:    effective.OIDCAdminTTL.Milliseconds(),
		CookieSecure:      effective.CookieSecure,
		MetricsPublic:     effective.MetricsPublic,
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
	if out.Settings.NotifyURLs != nil {
		out.Omitted = append(out.Omitted, "notify_urls")
		out.Settings.NotifyURLs = nil
	}
	if out.Settings.IngestToken != nil {
		out.Omitted = append(out.Omitted, "ingest_token")
		out.Settings.IngestToken = nil
	}

	// Downloaded rather than rendered: this is a file someone keeps.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "silt-settings-"+filenameSafe(cfg.HostName)+".json"))
	writeJSON(w, http.StatusOK, out)
}

// settingsUpdateRequest is a sparse patch. An absent field is left alone; a
// field named in reset drops its override and falls back to the environment.
type settingsUpdateRequest struct {
	settings.Overrides
	Reset []string `json:"reset,omitempty"`
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "settings are read-only in this configuration")
		return
	}

	var req settingsUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// The notification filter is validated here rather than in config, which
	// has no business knowing what a change kind is. Rejecting it before the
	// write means a typo'd severity never reaches the collector.
	next := req.Overrides
	if next.NotifyOn != nil || next.NotifyMinSeverity != nil {
		current := s.conf()
		kinds := current.NotifyOn
		if next.NotifyOn != nil {
			kinds = *next.NotifyOn
		}
		severity := current.NotifyMinSeverity
		if next.NotifyMinSeverity != nil {
			severity = *next.NotifyMinSeverity
		}
		if _, err := notify.ParseFilter(kinds, severity); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if _, err := s.live.Update(r.Context(), next, req.Reset); err != nil {
		if errors.Is(err, settings.ErrReadOnly) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fields := changedFields(next)
	s.log.Info("settings updated", "fields", fields, "reset", req.Reset)
	// Field names, never values. This table holds an ingest token and
	// notification URLs; recording what they became would put credentials in
	// a table built to be read.
	s.audit(r, store.AuditSettingsChanged, map[string]any{"fields": fields, "reset": req.Reset})
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

// changedFields names what a patch touched, for the audit line. The values
// themselves are deliberately absent: one of them can be a notification URL or
// the ingest token.
func changedFields(o settings.Overrides) []string {
	set := o.Set()
	out := make([]string, 0, len(set))
	for _, name := range settings.Fields {
		if set[name] {
			out = append(out, name)
		}
	}
	return out
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
