package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	RetentionIntervalMS    int64    `json:"retention_interval_ms"`
	VacuumIntervalMS       int64    `json:"vacuum_interval_ms"`
	KeepKeys               []string `json:"keep_keys"`
	BaseURL                string   `json:"base_url"`
	LogLevel               string   `json:"log_level"`
	NotifyTargets          []string `json:"notify_targets"`
	NotifyOn               []string `json:"notify_on"`
	NotifyMinSeverity      string   `json:"notify_min_severity"`
	IngestConfigured       bool     `json:"ingest_configured"`
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
	Editable bool          `json:"editable"`
	Fixed    settingsFixed `json:"fixed"`
	Usage    settingsUsage `json:"usage"`
}

func toValues(c config.Config) settingsValues {
	v := settingsValues{
		SnapshotIntervalMS:     c.SnapshotInterval.Milliseconds(),
		RetentionDays:          c.RetentionDays,
		UnchangedRetentionDays: c.UnchangedRetentionDays,
		EventRetentionDays:     c.EventRetentionDays,
		RetentionIntervalMS:    c.RetentionInterval.Milliseconds(),
		VacuumIntervalMS:       c.VacuumInterval.Milliseconds(),
		KeepKeys:               c.KeepKeys,
		BaseURL:                c.BaseURL,
		LogLevel:               c.LogLevel,
		NotifyTargets:          notify.MaskAll(c.NotifyURLs),
		NotifyOn:               c.NotifyOn,
		NotifyMinSeverity:      c.NotifyMinSeverity,
		IngestConfigured:       c.IngestToken != "",
	}
	if v.KeepKeys == nil {
		v.KeepKeys = []string{}
	}
	if v.NotifyOn == nil {
		v.NotifyOn = []string{}
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

	s.log.Info("settings updated", "fields", changedFields(next), "reset", req.Reset)
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
	}
	stats, err := s.store.Prune(r.Context(), policy, time.Now())
	if err != nil {
		s.log.Error("manual prune failed", "error", err)
		writeError(w, http.StatusInternalServerError, "prune failed")
		return
	}
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
