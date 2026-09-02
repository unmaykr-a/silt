package api_test

import (
	"encoding/json"
	"testing"
)

type overviewResponse struct {
	Projects []struct {
		ID              int64  `json:"id"`
		Name            string `json:"name"`
		Archived        bool   `json:"archived"`
		LastSeenAt      int64  `json:"last_seen_at"`
		LastChangedAt   int64  `json:"last_changed_at"`
		SnapshotID      int64  `json:"snapshot_id"`
		Services        int    `json:"services"`
		Running         int    `json:"running"`
		Stopped         int    `json:"stopped"`
		Restarting      int    `json:"restarting"`
		Paused          int    `json:"paused"`
		Unhealthy       int    `json:"unhealthy"`
		Crashed         int    `json:"crashed"`
		OOMKilled       int    `json:"oom_killed"`
		MaxRestartCount int    `json:"max_restart_count"`
		Drift           bool   `json:"drift"`
		Attention       bool   `json:"attention"`
	} `json:"projects"`
	Totals struct {
		Projects   int `json:"projects"`
		Services   int `json:"services"`
		Running    int `json:"running"`
		Stopped    int `json:"stopped"`
		Restarting int `json:"restarting"`
		Paused     int `json:"paused"`
		Unhealthy  int `json:"unhealthy"`
		Crashed    int `json:"crashed"`
		OOMKilled  int `json:"oom_killed"`
		Drift      int `json:"drift"`
		Restarts   int `json:"restarts"`
		Attention  int `json:"attention"`
	} `json:"totals"`
}

func (f *fixture) overview(t *testing.T) overviewResponse {
	t.Helper()
	resp, body := f.get(t, "/api/overview")
	if resp.StatusCode != 200 {
		t.Fatalf("overview = %d %s", resp.StatusCode, body)
	}
	var out overviewResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func TestOverviewReportsEveryProjectOnce(t *testing.T) {
	f := newFixture(t)
	got := f.overview(t)
	if len(got.Projects) != 1 {
		t.Fatalf("projects = %d, want 1: %+v", len(got.Projects), got.Projects)
	}
	if got.Projects[0].Name != "media" {
		t.Errorf("name = %q, want media", got.Projects[0].Name)
	}
}

// The fixture's last observation has radarr restarting, which is exactly the
// case the screen exists to surface — and it is reported as restarting rather
// than folded into "stopped", which is the distinction that matters: a crash
// loop and a container someone stopped are different problems.
func TestOverviewSurfacesAStackThatIsNotRunning(t *testing.T) {
	f := newFixture(t)
	p := f.overview(t).Projects[0]

	if p.Services != 1 {
		t.Fatalf("services = %d, want 1", p.Services)
	}
	if p.Running != 0 {
		t.Errorf("running = %d, want 0", p.Running)
	}
	if p.Restarting != 1 {
		t.Errorf("restarting = %d, want 1", p.Restarting)
	}
	if p.Stopped != 0 {
		t.Errorf("stopped = %d; a restarting container has not stopped", p.Stopped)
	}
	if p.Unhealthy != 0 {
		t.Errorf("unhealthy = %d; a restarting container is not an unhealthy one", p.Unhealthy)
	}
	if !p.Attention {
		t.Error("a stack whose only service is crash-looping does not want attention")
	}
}

// Attention is computed server-side so the badge count and the row highlight
// cannot disagree about what a problem is.
func TestOverviewTotalsAgreeWithTheRows(t *testing.T) {
	f := newFixture(t)
	got := f.overview(t)

	var attention, services, restarting int
	for _, p := range got.Projects {
		if p.Archived {
			continue
		}
		services += p.Services
		restarting += p.Restarting
		if p.Attention {
			attention++
		}
	}
	if got.Totals.Attention != attention {
		t.Errorf("totals.attention = %d, rows say %d", got.Totals.Attention, attention)
	}
	if got.Totals.Services != services {
		t.Errorf("totals.services = %d, rows say %d", got.Totals.Services, services)
	}
	if got.Totals.Restarting != restarting {
		t.Errorf("totals.restarting = %d, rows say %d", got.Totals.Restarting, restarting)
	}
}

// The screen asks "when did this last change", which on a stack observed every
// five minutes is not the same as when it was last seen.
func TestOverviewReportsLastChangedSeparately(t *testing.T) {
	f := newFixture(t)
	p := f.overview(t).Projects[0]
	if p.LastChangedAt == 0 {
		t.Error("last_changed_at is zero for a project that has changed")
	}
	if p.SnapshotID == 0 {
		t.Error("snapshot_id is zero for a snapshotted project")
	}
}

func TestOverviewReturnsAnArrayNotNull(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/overview")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// A null here becomes `projects.map is not a function` in the browser.
	if string(body) == "" || !json.Valid(body) {
		t.Fatalf("invalid body: %s", body)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	if string(raw["projects"]) == "null" {
		t.Error("projects is null; want an array")
	}
}
