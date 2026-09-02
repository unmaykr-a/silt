package store_test

import (
	"context"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/store"
)

func overviewOf(t *testing.T, db *store.Store, name string) store.ProjectOverview {
	t.Helper()
	rows, err := db.Overview(context.Background(), 1)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	for _, p := range rows {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no project named %q in %+v", name, rows)
	return store.ProjectOverview{}
}

// withFiles attaches captured compose files to an observation, which is what
// the drift derivation compares.
func withFiles(obs compose.Observation, content string) compose.Observation {
	obs.Files = []compose.CapturedFile{{
		Path:      "/srv/media/compose.yaml",
		Status:    compose.FileOK,
		Content:   []byte(content),
		LineCount: 1,
		Size:      int64(len(content)),
	}}
	return obs
}

func TestOverviewCountsServiceStates(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	writeSnap(t, db, id, observation(t, r, serviceOpts{state: "exited", restartCount: 4, exitCode: intp(137)}))

	got := overviewOf(t, db, "media")
	if got.Services != 1 {
		t.Errorf("services = %d, want 1", got.Services)
	}
	if got.Running != 0 || got.Stopped != 1 {
		t.Errorf("running/stopped = %d/%d, want 0/1", got.Running, got.Stopped)
	}
	if got.Crashed != 1 {
		t.Errorf("crashed = %d, want 1", got.Crashed)
	}
	if got.Unhealthy != 0 {
		t.Errorf("unhealthy = %d; a stopped container is not an unhealthy one", got.Unhealthy)
	}
	if got.MaxRestartCount != 4 {
		t.Errorf("max restart count = %d, want 4", got.MaxRestartCount)
	}
	if !got.Attention() {
		t.Error("a crashed stack does not want attention")
	}
}

// A container with no healthcheck reports an empty health, which is not the
// same as failing one. Counting it as unhealthy would light up most homelabs.
func TestOverviewDoesNotCallAMissingHealthcheckUnhealthy(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	writeSnap(t, db, id, observation(t, r, serviceOpts{state: "running", health: ""}))

	if got := overviewOf(t, db, "media"); got.Unhealthy != 0 {
		t.Errorf("unhealthy = %d, want 0", got.Unhealthy)
	}
}

func TestOverviewHealthyStackWantsNoAttention(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	writeSnap(t, db, id, observation(t, r, serviceOpts{state: "running", health: "healthy"}))

	got := overviewOf(t, db, "media")
	if got.Attention() {
		t.Errorf("healthy stack wants attention: %+v", got)
	}
}

// Editing a compose file without applying it is drift.
func TestOverviewReportsDrift(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{}), "services:\n  app:\n"))
	if got := overviewOf(t, db, "media"); got.Drift {
		t.Fatalf("drift reported on a freshly applied stack: %+v", got)
	}

	// The file changes; nothing restarts.
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{}), "services:\n  app:\n    cpus: 2\n"))
	if got := overviewOf(t, db, "media"); !got.Drift {
		t.Fatalf("edited compose file did not register as drift: %+v", got)
	}
}

// The regression this derivation exists for: drift used to be read off the
// latest snapshot's own files_changed flag, so any unrelated container restart
// afterwards produced a snapshot with files_changed=0 and the warning vanished
// while the file was still un-applied.
func TestOverviewDriftSurvivesAnUnrelatedRestart(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	edited := "services:\n  app:\n    cpus: 2\n"
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{}), "services:\n  app:\n"))
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{}), edited))

	// A restart: runtime changes, the file does not, the config does not.
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{restartCount: 1}), edited))

	if got := overviewOf(t, db, "media"); !got.Drift {
		t.Errorf("drift cleared by an unrelated restart: %+v", got)
	}
}

// Applying the edit clears it.
func TestOverviewDriftClearsOnApply(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	edited := "services:\n  app:\n    image: app:2\n"
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{}), "services:\n  app:\n"))
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{}), edited))
	// `compose up`: the image the container runs changes, so the running
	// configuration changes, with the edited file in place.
	writeSnap(t, db, id, withFiles(observation(t, r, serviceOpts{imageID: "sha256:bbbb"}), edited))

	if got := overviewOf(t, db, "media"); got.Drift {
		t.Errorf("drift still reported after the edit was applied: %+v", got)
	}
}

// A project with no compose roots configured captures no files, and must not
// be reported as drifted for want of anything to compare.
func TestOverviewWithoutCapturedFilesReportsNoDrift(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	writeSnap(t, db, id, observation(t, r, serviceOpts{}))
	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:bbbb"}))

	if got := overviewOf(t, db, "media"); got.Drift {
		t.Errorf("drift reported for a project with no captured files: %+v", got)
	}
}

// last_changed_at is a different question from last_seen_at: a stack observed
// every five minutes and unchanged for a month should say so.
func TestOverviewSeparatesLastChangedFromLastSeen(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	first := writeSnap(t, db, id, observation(t, r, serviceOpts{}))
	// An identical observation touches the snapshot rather than inserting one.
	writeSnap(t, db, id, observation(t, r, serviceOpts{}))

	got := overviewOf(t, db, "media")
	if got.LastChangedAt != first.TakenAt {
		t.Errorf("last changed = %d, want %d", got.LastChangedAt, first.TakenAt)
	}
}

func TestOverviewIncludesAProjectWithNoSnapshots(t *testing.T) {
	db, _ := openTestStore(t)
	newProject(t, db)

	got := overviewOf(t, db, "media")
	if got.SnapshotID != 0 || got.Services != 0 {
		t.Errorf("unsnapshotted project has state: %+v", got)
	}
	if got.Attention() {
		t.Error("a project Silt has not snapshotted yet is not a problem to look at")
	}
}


// The distinction the counts exist for: an unhealthy container is running and
// answering wrongly, a stopped one is not running at all, and a restarting one
// is in a crash loop. Rolling them into a single "not running" number was why
// the screen could not say which you were looking at.
func TestOverviewKeepsFailureModesApart(t *testing.T) {
	cases := []struct {
		name string
		opts serviceOpts
		want store.ProjectOverview
	}{
		{
			name: "unhealthy is running",
			opts: serviceOpts{state: "running", health: "unhealthy"},
			want: store.ProjectOverview{Running: 1, Unhealthy: 1},
		},
		{
			name: "stopped cleanly is not a crash",
			opts: serviceOpts{state: "exited", exitCode: intp(0)},
			want: store.ProjectOverview{Stopped: 1},
		},
		{
			name: "stopped with a code is a crash",
			opts: serviceOpts{state: "exited", exitCode: intp(1)},
			want: store.ProjectOverview{Stopped: 1, Crashed: 1},
		},
		{
			name: "killed for memory",
			opts: serviceOpts{state: "exited", exitCode: intp(137), oomKilled: true},
			want: store.ProjectOverview{Stopped: 1, Crashed: 1, OOMKilled: 1},
		},
		{
			name: "restarting is its own thing",
			opts: serviceOpts{state: "restarting"},
			want: store.ProjectOverview{Restarting: 1},
		},
		{
			name: "paused is not stopped",
			opts: serviceOpts{state: "paused"},
			want: store.ProjectOverview{Paused: 1},
		},
		{
			name: "starting healthcheck is not unhealthy",
			opts: serviceOpts{state: "running", health: "starting"},
			want: store.ProjectOverview{Running: 1, Starting: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, r := openTestStore(t)
			id := newProject(t, db)
			writeSnap(t, db, id, observation(t, r, tc.opts))

			got := overviewOf(t, db, "media")
			if got.Running != tc.want.Running || got.Stopped != tc.want.Stopped ||
				got.Restarting != tc.want.Restarting || got.Paused != tc.want.Paused ||
				got.Starting != tc.want.Starting || got.Unhealthy != tc.want.Unhealthy ||
				got.Crashed != tc.want.Crashed || got.OOMKilled != tc.want.OOMKilled {
				t.Errorf("\n got running=%d stopped=%d restarting=%d paused=%d starting=%d unhealthy=%d crashed=%d oom=%d"+
					"\nwant running=%d stopped=%d restarting=%d paused=%d starting=%d unhealthy=%d crashed=%d oom=%d",
					got.Running, got.Stopped, got.Restarting, got.Paused, got.Starting, got.Unhealthy, got.Crashed, got.OOMKilled,
					tc.want.Running, tc.want.Stopped, tc.want.Restarting, tc.want.Paused, tc.want.Starting, tc.want.Unhealthy, tc.want.Crashed, tc.want.OOMKilled)
			}
		})
	}
}

// A container someone stopped on purpose is not a problem to go and look at.
// This is the case that made "everything not running is a problem" wrong.
func TestOverviewDeliberatelyStoppedWantsNoAttention(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	writeSnap(t, db, id, observation(t, r, serviceOpts{state: "exited", exitCode: intp(0)}))

	if got := overviewOf(t, db, "media"); got.Attention() {
		t.Errorf("a cleanly stopped stack wants attention: %+v", got)
	}
}

func intp(v int) *int { return &v }
