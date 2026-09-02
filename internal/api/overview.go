package api

import (
	"net/http"

	"github.com/unmaykr-a/silt/internal/store"
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

	Services int `json:"services"`
	Running  int `json:"running"`

	// The ways a container can fail to be running, kept apart. A container
	// someone stopped on purpose and a container in a crash loop have nothing
	// in common except that neither is running.
	Stopped    int `json:"stopped"`
	Restarting int `json:"restarting"`
	Paused     int `json:"paused"`
	Starting   int `json:"starting"`
	// Unhealthy counts running containers failing their healthcheck: the
	// process is up and answering wrongly, which is not the same failure as
	// not running at all.
	Unhealthy int `json:"unhealthy"`
	// Crashed counts stopped containers with a non-zero exit code — the ones
	// nobody asked to stop. OOMKilled is not derivable from the exit code: an
	// OOM kill and a `docker kill` are both 137.
	Crashed   int `json:"crashed"`
	OOMKilled int `json:"oom_killed"`
	// MaxRestartCount is the highest restart count among this stack's
	// containers, counted since each container was created — the number
	// `docker ps` shows, not a rate.
	MaxRestartCount int `json:"max_restart_count"`
	// RestartedAt is when the stack last actually restarted, or absent if it
	// never has. RecentRestarts says whether that is recent enough to still
	// count as something to look at: Docker's counter never resets, so a
	// single blip months ago would otherwise pin a stack to the attention
	// list forever.
	RestartedAt    int64 `json:"restarted_at,omitempty"`
	RecentRestarts bool  `json:"recent_restarts"`

	Drift bool `json:"drift"`
	// Attention is computed here rather than in the browser so the API and the
	// UI cannot come to disagree about what counts as a problem.
	Attention bool `json:"attention"`
}

// Container-level totals count containers; project-level totals count
// projects. Which is which is stated per field, because a mixed set where the
// reader has to guess is how a dashboard starts lying.
type overviewTotals struct {
	Projects int `json:"projects"`
	Services int `json:"services"`
	// Containers.
	Running    int `json:"running"`
	Stopped    int `json:"stopped"`
	Restarting int `json:"restarting"`
	Paused     int `json:"paused"`
	Unhealthy  int `json:"unhealthy"`
	Crashed    int `json:"crashed"`
	OOMKilled  int `json:"oom_killed"`
	// Projects.
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

	// One clock for the whole response, so two projects cannot land on
	// opposite sides of the restart window inside a single render.
	now := store.Now()

	out := overviewResponse{Projects: make([]overviewProject, 0, len(rows))}
	for _, p := range rows {
		item := overviewProject{
			ID:              p.ID,
			Name:            p.Name,
			WorkingDir:      p.WorkingDir,
			Archived:        p.Archived,
			LastSeenAt:      p.LastSeenAt,
			LastChangedAt:   p.LastChangedAt,
			SnapshotID:      p.SnapshotID,
			Services:        p.Services,
			Running:         p.Running,
			Stopped:         p.Stopped,
			Restarting:      p.Restarting,
			Paused:          p.Paused,
			Starting:        p.Starting,
			Unhealthy:       p.Unhealthy,
			Crashed:         p.Crashed,
			OOMKilled:       p.OOMKilled,
			MaxRestartCount: p.MaxRestartCount,
			RestartedAt:     p.RestartedAt,
			RecentRestarts:  p.RestartsAreRecent(now),
			Drift:           p.Drift,
			Attention:       p.Attention(),
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
		out.Totals.Restarting += p.Restarting
		out.Totals.Paused += p.Paused
		out.Totals.Unhealthy += p.Unhealthy
		out.Totals.Crashed += p.Crashed
		out.Totals.OOMKilled += p.OOMKilled
		if p.Drift {
			out.Totals.Drift++
		}
		if p.RestartsAreRecent(now) {
			out.Totals.Restarts++
		}
		if p.Attention() {
			out.Totals.Attention++
		}
	}
	writeJSON(w, http.StatusOK, out)
}
