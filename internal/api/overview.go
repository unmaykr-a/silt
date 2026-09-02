package api

import (
	"net/http"
)

// The fleet view.
//
// One request answers the question the Projects screen could not: across every
// stack on this host, what is down, what is unhealthy, what has been
// restarting, and what was edited but never applied. Doing it per project would
// be forty-seven requests to render one screen.

type overviewProject struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	WorkingDir string `json:"working_dir,omitempty"`
	Archived   bool   `json:"archived"`
	LastSeenAt int64  `json:"last_seen_at"`
	// LastChangedAt is zero when nothing has changed since Silt first saw the
	// project, or when the snapshot that changed it has been pruned.
	LastChangedAt int64 `json:"last_changed_at,omitempty"`
	SnapshotID    int64 `json:"snapshot_id,omitempty"`

	Services  int `json:"services"`
	Running   int `json:"running"`
	Stopped   int `json:"stopped"`
	Unhealthy int `json:"unhealthy"`
	// Restarts is the highest restart count among this stack's containers,
	// counted since each container was created — the number `docker ps` shows,
	// not a rate.
	Restarts int `json:"restarts"`

	Drift bool `json:"drift"`
	// Attention is computed here rather than in the browser so the API and the
	// UI cannot come to disagree about what counts as a problem.
	Attention bool `json:"attention"`
}

type overviewTotals struct {
	Projects  int `json:"projects"`
	Services  int `json:"services"`
	Running   int `json:"running"`
	Stopped   int `json:"stopped"`
	Unhealthy int `json:"unhealthy"`
	Drift     int `json:"drift"`
	Restarts  int `json:"restarts"`
	Attention int `json:"attention"`
}

type overviewResponse struct {
	Projects []overviewProject `json:"projects"`
	Totals   overviewTotals    `json:"totals"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	hostID := queryInt(r, "host", 0)
	if hostID == 0 {
		hosts, err := s.store.RQ.ListHosts(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read hosts")
			return
		}
		if len(hosts) == 0 {
			writeJSON(w, http.StatusOK, overviewResponse{Projects: []overviewProject{}})
			return
		}
		hostID = hosts[0].ID
	}

	rows, err := s.store.Overview(r.Context(), hostID)
	if err != nil {
		s.log.Error("overview failed", "error", err)
		writeError(w, http.StatusInternalServerError, "read overview")
		return
	}

	out := overviewResponse{Projects: make([]overviewProject, 0, len(rows))}
	for _, p := range rows {
		item := overviewProject{
			ID:            p.ID,
			Name:          p.Name,
			WorkingDir:    p.WorkingDir,
			Archived:      p.Archived,
			LastSeenAt:    p.LastSeenAt,
			LastChangedAt: p.LastChangedAt,
			SnapshotID:    p.SnapshotID,
			Services:      p.Services,
			Running:       p.Running,
			Stopped:       p.Stopped,
			Unhealthy:     p.Unhealthy,
			Restarts:      p.Restarts,
			Drift:         p.Drift,
			Attention:     p.Attention(),
		}
		out.Projects = append(out.Projects, item)

		// Archived projects are counted in the list but not in the totals: a
		// stack that was removed from the host months ago is not something to
		// go and look at, and counting it would make the badge permanently
		// non-zero for a host that is entirely healthy.
		if p.Archived {
			continue
		}
		out.Totals.Projects++
		out.Totals.Services += p.Services
		out.Totals.Running += p.Running
		out.Totals.Stopped += p.Stopped
		out.Totals.Unhealthy += p.Unhealthy
		if p.Drift {
			out.Totals.Drift++
		}
		if p.Restarts > 0 {
			out.Totals.Restarts++
		}
		if p.Attention() {
			out.Totals.Attention++
		}
	}
	writeJSON(w, http.StatusOK, out)
}
