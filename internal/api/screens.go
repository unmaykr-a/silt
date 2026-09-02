package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"

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
	// ExitCode is present only for an observation where the container had
	// stopped. Absent means "still running", not "exited cleanly" — Docker
	// reports the previous run's code while a container is up, and rendering
	// that as the current state says the wrong thing about a healthy service.
	ExitCode  *int64 `json:"exit_code,omitempty"`
	OOMKilled bool   `json:"oom_killed,omitempty"`
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
			ExitCode:      nullableInt64(row.ExitCode),
			OOMKilled:     row.OomKilled != 0,
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

// nullableInt64 keeps SQL NULL distinct from zero across the JSON boundary,
// which for an exit code is the whole point: 0 means "exited cleanly" and NULL
// means "did not exit".
func nullableInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
