package diff_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/diff"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

type proj struct{ name string }

func (p proj) ProjectName() string       { return p.name }
func (p proj) ProjectWorkingDir() string { return "/srv/" + p.name }
func (p proj) ConfigFiles() []string     { return []string{} }

func toDiffInput(m store.SnapshotModel) diff.Input {
	runtimes := make(map[string]diff.Runtime, len(m.Runtimes))
	for name, rt := range m.Runtimes {
		runtimes[name] = diff.Runtime{
			State:        rt.State,
			Health:       rt.Health,
			RestartCount: rt.RestartCount,
		}
	}
	return diff.Input{
		Side:     diff.Side{SnapshotID: m.Snapshot.ID, TakenAt: m.Snapshot.TakenAt},
		Project:  m.Project,
		Runtimes: runtimes,
	}
}

// The engine must work against snapshots that have made a full round trip
// through redaction, canonical JSON, zstd and SQLite — not only against
// hand-built fixtures, where a serialisation bug would be invisible.
func TestDiffOverStoredSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	key, _ := db.RedactionKey(ctx)
	r := redact.New(key, nil)

	build := func(imageID, apiKey, state string, restarts int) compose.Observation {
		obs, err := compose.Build(
			docker.Project{Name: "media", WorkingDir: "/srv/media"},
			[]compose.ServiceInput{{
				Service: "radarr",
				Inspected: docker.Inspected{
					Config: docker.ContainerConfig{
						Image:   "lscr.io/linuxserver/radarr:latest",
						ImageID: imageID,
						Env:     []string{"PUID=1000", "API_KEY=" + apiKey},
						Mounts: []docker.Mount{
							{Type: "bind", Source: "/srv/media/radarr", Target: "/config", Mode: "rw"},
						},
					},
					Runtime: docker.RuntimeState{
						ContainerID:  "c1",
						State:        state,
						Health:       "healthy",
						RestartCount: restarts,
					},
				},
			}},
			r,
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return obs
	}

	_, projectID, err := db.UpsertHostAndProject(ctx, "local", "tcp://proxy:2375", "28.0", proj{"media"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	first, err := db.WriteSnapshot(ctx, projectID, store.Now(), "manual", build("sha256:aaaa", "old-secret", "running", 0))
	if err != nil {
		t.Fatalf("write first: %v", err)
	}
	second, err := db.WriteSnapshot(ctx, projectID, store.Now(), "manual", build("sha256:bbbb", "new-secret", "restarting", 2))
	if err != nil {
		t.Fatalf("write second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("second write touched the first snapshot instead of inserting")
	}

	fromModel, err := db.LoadSnapshotModel(ctx, first.ID)
	if err != nil {
		t.Fatalf("load first: %v", err)
	}
	toModel, err := db.LoadSnapshotModel(ctx, second.ID)
	if err != nil {
		t.Fatalf("load second: %v", err)
	}

	res := diff.Compute(toDiffInput(fromModel), toDiffInput(toModel))

	if res.Summary[diff.KindImageID] != 1 {
		t.Errorf("image_id change missing: %+v", res.Changes)
	}
	if res.Summary[diff.KindEnv] != 1 {
		t.Errorf("env change missing: %+v", res.Changes)
	}
	if res.Summary[diff.KindState] < 2 {
		t.Errorf("state and restart_count changes missing: %+v", res.Changes)
	}

	// PUID did not change and must not be reported.
	for _, c := range res.Changes {
		if strings.HasSuffix(c.Path, ".PUID") {
			t.Errorf("unchanged PUID was reported as a change: %+v", c)
		}
	}

	// The secret changed, and the diff must prove it changed without holding
	// either value.
	for _, c := range res.Changes {
		if !strings.HasSuffix(c.Path, ".API_KEY") {
			continue
		}
		if strings.Contains(c.Before, "secret") || strings.Contains(c.After, "secret") {
			t.Errorf("diff leaked a secret value: %+v", c)
		}
		if c.Before == c.After {
			t.Errorf("API_KEY change reported identical digests: %+v", c)
		}
	}

	// The bind mount is unchanged between the two snapshots, so redacted
	// sources must compare equal rather than looking different every time.
	if res.Summary[diff.KindVolumes] != 0 {
		t.Errorf("unchanged volume reported as changed: %+v", res.Changes)
	}
}
