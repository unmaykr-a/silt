package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// Endpoints backing the Project, Service and Settings screens. These are
// additions to the surface sketched in PROJECT.md Section 8: Section 9's
// screens need data no listed endpoint returns, and assembling a service's
// history client-side would be one request per snapshot.

// serviceObservation is one point in a service's history.
type serviceObservation struct {
	SnapshotID    int64  `json:"snapshot_id"`
	TakenAt       int64  `json:"taken_at"`
	ConfigChanged bool   `json:"config_changed"`
	ImageRef      string `json:"image_ref,omitempty"`
	ImageID       string `json:"image_id,omitempty"`
	ImageDigest   string `json:"image_digest,omitempty"`
	State         string `json:"state,omitempty"`
	Health        string `json:"health,omitempty"`
	RestartCount  int64  `json:"restart_count"`
}

// envKeyChange is one observed transition of a single environment key.
//
// Values are keyed digests unless the key is on the cleartext keep-list, so
// this answers "when did SECRET_KEY last change?" without the history ever
// holding the secret.
type envKeyChange struct {
	Key       string `json:"key"`
	TakenAt   int64  `json:"taken_at"`
	Redacted  bool   `json:"redacted"`
	Digest    string `json:"digest"`
	Bucket    string `json:"value_len_bucket"`
	Value     string `json:"value,omitempty"`
	FirstSeen bool   `json:"first_seen"`
}

type serviceHistoryResponse struct {
	Project      int64                `json:"project_id"`
	Service      string               `json:"service"`
	Observations []serviceObservation `json:"observations"`
	EnvChanges   []envKeyChange       `json:"env_changes"`
}

func (s *Server) getServiceHistory(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	service := r.PathValue("service")
	if service == "" {
		writeError(w, http.StatusBadRequest, "service is required")
		return
	}
	if _, err := s.store.RQ.GetProject(r.Context(), projectID); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	limit := queryLimit(r, 200, 1000)
	rows, err := s.store.RQ.ServiceHistory(r.Context(), sqlcgen.ServiceHistoryParams{
		ProjectID: projectID,
		Service:   service,
		MaxRows:   limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read service history")
		return
	}

	out := serviceHistoryResponse{
		Project:      projectID,
		Service:      service,
		Observations: make([]serviceObservation, 0, len(rows)),
		EnvChanges:   []envKeyChange{},
	}
	for _, row := range rows {
		out.Observations = append(out.Observations, serviceObservation{
			SnapshotID:    row.SnapshotID,
			TakenAt:       row.TakenAt,
			ConfigChanged: row.ConfigChanged == 1,
			ImageRef:      row.ImageRef,
			ImageID:       row.ImageID,
			ImageDigest:   row.ImageDigest,
			State:         row.State,
			Health:        row.Health,
			RestartCount:  row.RestartCount,
		})
	}

	envRows, err := s.store.RQ.ServiceEnvHistory(r.Context(), sqlcgen.ServiceEnvHistoryParams{
		ProjectID: projectID,
		Service:   service,
		MaxRows:   limit * 32,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read env history")
		return
	}
	out.EnvChanges = foldEnvChanges(envRows)

	writeJSON(w, http.StatusOK, out)
}

// foldEnvChanges reduces per-observation env rows to the points where a key's
// value actually changed.
//
// The rows arrive newest first, so walking them in reverse gives chronological
// order and the first sighting of each key is flagged rather than reported as
// a change from nothing.
func foldEnvChanges(rows []sqlcgen.ServiceEnvHistoryRow) []envKeyChange {
	lastDigest := map[string]string{}
	out := []envKeyChange{}

	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		prev, seen := lastDigest[row.Key]
		if seen && prev == row.ValueHmac {
			continue
		}
		lastDigest[row.Key] = row.ValueHmac
		out = append(out, envKeyChange{
			Key:       row.Key,
			TakenAt:   row.TakenAt,
			Redacted:  row.Redacted == 1,
			Digest:    row.ValueHmac,
			Bucket:    row.ValueLenBucket,
			Value:     row.Value.String,
			FirstSeen: !seen,
		})
	}

	// Newest first, matching every other listing.
	sort.SliceStable(out, func(i, j int) bool { return out[i].TakenAt > out[j].TakenAt })
	return out
}

// settingsResponse is the effective configuration, read-only.
//
// Silt is configured by environment variables, so this reports what is in
// force rather than offering to change it — a settings screen that wrote to a
// database Silt does not read from would be a lie. Secrets are reported as
// set-or-not, never echoed.
type settingsResponse struct {
	Version                string   `json:"version"`
	HostName               string   `json:"host_name"`
	DockerHost             string   `json:"docker_host"`
	DBPath                 string   `json:"db_path"`
	SnapshotIntervalMS     int64    `json:"snapshot_interval_ms"`
	RetentionDays          int      `json:"retention_days"`
	UnchangedRetentionDays int      `json:"unchanged_retention_days"`
	EventRetentionDays     int      `json:"event_retention_days"`
	RetentionIntervalMS    int64    `json:"retention_interval_ms"`
	VacuumIntervalMS       int64    `json:"vacuum_interval_ms"`
	KeepKeys               []string `json:"keep_keys"`
	IngestConfigured       bool     `json:"ingest_configured"`
	LogLevel               string   `json:"log_level"`
	Usage                  struct {
		Blobs             int64 `json:"blobs"`
		StoredBytes       int64 `json:"stored_bytes"`
		UncompressedBytes int64 `json:"uncompressed_bytes"`
		Events            int64 `json:"events"`
	} `json:"usage"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	out := settingsResponse{
		Version:                s.version,
		HostName:               s.cfg.HostName,
		DockerHost:             s.cfg.DockerHost,
		DBPath:                 s.cfg.DBPath,
		SnapshotIntervalMS:     s.cfg.SnapshotInterval.Milliseconds(),
		RetentionDays:          s.cfg.RetentionDays,
		UnchangedRetentionDays: s.cfg.UnchangedRetentionDays,
		EventRetentionDays:     s.cfg.EventRetentionDays,
		RetentionIntervalMS:    s.cfg.RetentionInterval.Milliseconds(),
		VacuumIntervalMS:       s.cfg.VacuumInterval.Milliseconds(),
		KeepKeys:               s.cfg.KeepKeys,
		// Never echo the token itself.
		IngestConfigured: s.cfg.IngestToken != "",
		LogLevel:         s.cfg.LogLevel,
	}
	if out.KeepKeys == nil {
		out.KeepKeys = []string{}
	}

	if usage, err := s.store.Usage(r.Context()); err == nil {
		out.Usage.Blobs = usage.Blobs
		out.Usage.StoredBytes = usage.StoredBytes
		out.Usage.UncompressedBytes = usage.UncompressedBytes
	}
	if events, err := s.store.RQ.CountEvents(r.Context()); err == nil {
		out.Usage.Events = events
	}

	writeJSON(w, http.StatusOK, out)
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
	policy := store.RetentionPolicy{
		Changed:   config.Days(s.cfg.RetentionDays),
		Unchanged: config.Days(s.cfg.UnchangedRetentionDays),
		Events:    config.Days(s.cfg.EventRetentionDays),
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

// listProjectServices backs the Project screen's service navigation.
func (s *Server) listProjectServices(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.store.RQ.ProjectServices(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read services")
		return
	}
	if rows == nil {
		rows = []string{}
	}
	writeJSON(w, http.StatusOK, rows)
}
