package collect_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"

	"github.com/unmaykr-a/silt/internal/collect"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/docker/dockertest"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

// The snapshot pipeline, end to end, against a fake engine.
//
// This package sat at 22% coverage because the fake engine was a test-only
// type in another package — and it is the package that has actually shipped a
// bug: snapshots written and never broadcast, so a project screen showed
// yesterday until you reloaded. These tests exist so that class is caught by
// something other than a person noticing.

// recorder captures what the snapshotter broadcasts.
type recorder struct {
	mu      sync.Mutex
	changes []map[string]any
	events  []any
}

func (r *recorder) PublishChange(payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := payload.(map[string]any); ok {
		r.changes = append(r.changes, m)
	}
}

func (r *recorder) PublishEvent(payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, payload)
}

func (r *recorder) broadcasts() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.changes...)
}

type harness struct {
	engine *dockertest.Engine
	client *docker.Client
	db     *store.Store
	snap   *collect.Snapshotter
	pub    *recorder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	engine := dockertest.New()
	t.Cleanup(engine.Close)

	client, err := docker.New(engine.Host())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := db.RedactionKey(ctx)
	if err != nil {
		t.Fatalf("redaction key: %v", err)
	}

	pub := &recorder{}
	return &harness{
		engine: engine,
		client: client,
		db:     db,
		pub:    pub,
		snap: &collect.Snapshotter{
			Client:    client,
			Store:     db,
			Redactor:  redact.New(key, nil),
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			HostName:  "local",
			Endpoint:  engine.Host(),
			Publisher: pub,
		},
	}
}

// serve registers one container as the whole of a project.
func (h *harness) serve(project, service, imageRef, imageID string, env []string, mutate func(*container.InspectResponse)) {
	id := project + "-" + service
	h.engine.SetContainers([]container.Summary{
		dockertest.Container(id, project, service, imageRef, "/srv/"+project),
	})
	inspect := dockertest.Inspect(id, project+"-"+service+"-1", imageRef, imageID, env)
	if mutate != nil {
		mutate(&inspect)
	}
	h.engine.SetInspect(id, inspect)
	h.engine.SetImage(imageRef, dockertest.Image(imageID, []string{imageRef + "@sha256:beef"}))
}

// snapshot discovers and snapshots, failing the test on any error.
func (h *harness) snapshot(t *testing.T, trigger string) store.SnapshotResult {
	t.Helper()
	ctx := context.Background()
	projects, err := h.client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("discovered %d projects, want 1", len(projects))
	}
	result, err := h.snap.Snapshot(ctx, projects[0], trigger)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return result
}

func TestSnapshotRecordsAndBroadcastsAChange(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa",
		[]string{"PUID=1000", "API_KEY=super-secret"}, nil)

	result := h.snapshot(t, "manual")
	if !result.ConfigChanged {
		t.Error("the first snapshot of a project should be a change")
	}

	sent := h.pub.broadcasts()
	if len(sent) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(sent))
	}
	if sent[0]["project"] != "media" {
		t.Errorf("broadcast names project %v, want media", sent[0]["project"])
	}
	if sent[0]["config_changed"] != true {
		t.Errorf("broadcast does not report the config change: %v", sent[0])
	}
}

// An identical observation must not be broadcast. On an idle host of forty
// projects that would be one message per project per interval to say nothing
// happened.
func TestAnUnchangedObservationIsSilent(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa", []string{"PUID=1000"}, nil)

	h.snapshot(t, "interval")
	before := len(h.pub.broadcasts())

	second := h.snapshot(t, "interval")
	if !second.Touched {
		t.Error("an identical observation should touch the existing snapshot")
	}
	if got := len(h.pub.broadcasts()); got != before {
		t.Errorf("broadcasts went from %d to %d for an observation where nothing changed", before, got)
	}
}

// The regression this package is here for: a runtime-only change is written
// and must be broadcast. It used to be silent, because a docker event was
// assumed to cover it — but the interval sweep produces no docker event, and
// the event that does exist is sent before the snapshot is written.
func TestARuntimeOnlyChangeIsBroadcast(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa", []string{"PUID=1000"}, nil)
	h.snapshot(t, "interval")
	before := len(h.pub.broadcasts())

	// Same image, same environment: only the health changed.
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa", []string{"PUID=1000"},
		func(r *container.InspectResponse) { r.State.Health.Status = "unhealthy" })

	result := h.snapshot(t, "interval")
	if result.ConfigChanged {
		t.Error("a health change should not read as a configuration change")
	}
	if !result.RuntimeChanged {
		t.Fatal("a health change was not recorded as a runtime change")
	}

	sent := h.pub.broadcasts()
	if len(sent) != before+1 {
		t.Fatalf("broadcasts = %d, want %d — a runtime change must reach the browser", len(sent), before+1)
	}
	last := sent[len(sent)-1]
	if last["runtime_changed"] != true || last["config_changed"] != false {
		t.Errorf("the broadcast misreports what changed: %v", last)
	}
}

func TestAnImageChangeIsAConfigChange(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa", []string{"PUID=1000"}, nil)
	h.snapshot(t, "interval")

	h.serve("media", "radarr", "radarr:5.5.0", "sha256:bbbb", []string{"PUID=1000"}, nil)
	result := h.snapshot(t, "interval")

	if !result.ConfigChanged {
		t.Error("a new image is a configuration change")
	}
}

// The whole point of the project, exercised through the real pipeline rather
// than against the redactor directly: a secret that passes through discovery,
// inspect, model building and the store must not be readable afterwards.
func TestASecretDoesNotSurviveThePipeline(t *testing.T) {
	const secret = "PIPELINE-SENTINEL-8f14e45fceea167a"
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa",
		[]string{"PUID=1000", "TZ=Europe/Tallinn", "API_KEY=" + secret}, nil)
	h.snapshot(t, "manual")

	ctx := context.Background()
	results, err := h.db.Search(ctx, secret, 25)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results.EnvKeys) != 0 || len(results.Files) != 0 || len(results.Events) != 0 {
		t.Errorf("the secret is findable after a full snapshot: %+v", results)
	}

	// The key itself is recorded — that is the feature — and its value is not.
	keys, err := h.db.Search(ctx, "API_KEY", 25)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(keys.EnvKeys) == 0 {
		t.Fatal("the environment key was not recorded at all")
	}
	for _, k := range keys.EnvKeys {
		if k.Readable {
			t.Errorf("API_KEY was stored in cleartext: %+v", k)
		}
	}
}

// A container listed and then removed before it is inspected is a race Silt
// meets on any host where something is restarting. It must not abort the whole
// project's snapshot.
func TestAContainerThatVanishesMidSnapshot(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaaa", []string{"PUID=1000"}, nil)
	h.engine.FailInspect("media-radarr")

	ctx := context.Background()
	projects, err := h.client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	// Whatever it does, it must not panic and must not hang.
	if _, err := h.snap.Snapshot(ctx, projects[0], "interval"); err != nil {
		t.Logf("snapshot reported: %v", err)
	}
}

// Discovery reads the project from Compose's own labels. Getting this wrong
// means every container becomes its own project.
func TestDiscoveryGroupsByComposeProject(t *testing.T) {
	h := newHarness(t)
	h.engine.SetContainers([]container.Summary{
		dockertest.Container("media-radarr", "media", "radarr", "radarr:5.4.0", "/srv/media"),
		dockertest.Container("media-sonarr", "media", "sonarr", "sonarr:4.0.1", "/srv/media"),
		dockertest.Container("dns-pihole", "dns", "pihole", "pihole:2024.07", "/srv/dns"),
	})

	projects, err := h.client.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("discovered %d projects, want 2 (media, dns)", len(projects))
	}
	for _, p := range projects {
		switch p.Name {
		case "media":
			if len(p.Services) != 2 {
				t.Errorf("media has %d services, want 2", len(p.Services))
			}
			if p.WorkingDir != "/srv/media" {
				t.Errorf("media working dir = %q", p.WorkingDir)
			}
		case "dns":
			if len(p.Services) != 1 {
				t.Errorf("dns has %d services, want 1", len(p.Services))
			}
		default:
			t.Errorf("unexpected project %q", p.Name)
		}
	}
}
