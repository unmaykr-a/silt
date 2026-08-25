package compose_test

import (
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
)

func testRedactor() *redact.Redactor {
	return redact.New([]byte("test-key"), nil)
}

func build(t *testing.T, cfg docker.ContainerConfig) compose.Observation {
	t.Helper()
	obs, err := compose.Build(
		docker.Project{Name: "media"},
		[]compose.ServiceInput{{Service: "app", Inspected: docker.Inspected{Config: cfg}}},
		testRedactor(),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return obs
}

// Canonical output must be byte-identical for equivalent input regardless of
// map iteration order. Without this every observation differs from the last
// and Silt becomes a noise generator.
func TestCanonicalJSONIsStableAcrossMapOrder(t *testing.T) {
	cfg := docker.ContainerConfig{
		Image: "example/app:latest",
		Env:   []string{"A=1", "B=2", "C=3", "D=4", "E=5"},
		Labels: map[string]string{
			"com.docker.compose.project": "media",
			"com.docker.compose.service": "app",
			"org.label-schema.name":      "app",
		},
	}

	first, err := compose.CanonicalJSON(build(t, cfg).Project)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Rebuild many times; Go randomises map iteration, so a non-canonical
	// encoder would diverge within a few rounds.
	for i := 0; i < 50; i++ {
		got, err := compose.CanonicalJSON(build(t, cfg).Project)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("canonical JSON diverged on round %d:\n first: %s\n  got:  %s", i, first, got)
		}
	}
}

func TestConfigFingerprintIgnoresRuntime(t *testing.T) {
	runtimes := []compose.ServiceRuntime{
		{Service: "app", ImageID: "sha256:aaaa", InspectHash: "hash1", State: "running", RestartCount: 0},
	}
	restarted := []compose.ServiceRuntime{
		{Service: "app", ImageID: "sha256:aaaa", InspectHash: "hash1", State: "restarting", RestartCount: 7},
	}

	if compose.ConfigFingerprint("c1", runtimes) != compose.ConfigFingerprint("c1", restarted) {
		t.Error("config fingerprint changed on a pure runtime difference")
	}
	if compose.RuntimeFingerprint(runtimes) == compose.RuntimeFingerprint(restarted) {
		t.Error("runtime fingerprint ignored a restart")
	}
}

func TestConfigFingerprintTracksImageAndInspect(t *testing.T) {
	base := []compose.ServiceRuntime{{Service: "app", ImageID: "sha256:aaaa", InspectHash: "hash1"}}
	newImage := []compose.ServiceRuntime{{Service: "app", ImageID: "sha256:bbbb", InspectHash: "hash1"}}
	newInspect := []compose.ServiceRuntime{{Service: "app", ImageID: "sha256:aaaa", InspectHash: "hash2"}}

	if compose.ConfigFingerprint("c1", base) == compose.ConfigFingerprint("c1", newImage) {
		t.Error("config fingerprint ignored a new image ID")
	}
	if compose.ConfigFingerprint("c1", base) == compose.ConfigFingerprint("c1", newInspect) {
		t.Error("config fingerprint ignored a changed inspect blob")
	}
	if compose.ConfigFingerprint("c1", base) == compose.ConfigFingerprint("c2", base) {
		t.Error("config fingerprint ignored a changed compose blob")
	}
}

// Service order must not affect either fingerprint.
func TestFingerprintsAreOrderIndependent(t *testing.T) {
	a := []compose.ServiceRuntime{
		{Service: "app", ImageID: "sha256:a", InspectHash: "h1", State: "running"},
		{Service: "db", ImageID: "sha256:b", InspectHash: "h2", State: "running"},
	}
	b := []compose.ServiceRuntime{a[1], a[0]}

	if compose.ConfigFingerprint("c", a) != compose.ConfigFingerprint("c", b) {
		t.Error("config fingerprint depends on service order")
	}
	if compose.RuntimeFingerprint(a) != compose.RuntimeFingerprint(b) {
		t.Error("runtime fingerprint depends on service order")
	}
}

func TestBuildRedactsBeforeModelExists(t *testing.T) {
	obs := build(t, docker.ContainerConfig{
		Image: "example/app:latest",
		Env:   []string{"TOKEN=supersecret", "PUID=1000"},
	})

	blob, err := compose.CanonicalJSON(obs.Project)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "supersecret") {
		t.Errorf("secret survived into the model: %s", blob)
	}
	if !strings.Contains(string(blob), "1000") {
		t.Errorf("kept value did not survive: %s", blob)
	}
}

// A project with no running containers is marked unavailable rather than
// silently recorded as an empty stack.
func TestEmptyProjectIsUnavailable(t *testing.T) {
	obs, err := compose.Build(docker.Project{Name: "media"}, nil, testRedactor())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if obs.Project.Source != compose.SourceUnavailable {
		t.Errorf("source = %q, want %q", obs.Project.Source, compose.SourceUnavailable)
	}
}

func TestBindMountSourcesAreRedactedButStructureSurvives(t *testing.T) {
	obs := build(t, docker.ContainerConfig{
		Mounts: []docker.Mount{
			{Type: "bind", Source: "/srv/media/config", Target: "/config", Mode: "rw"},
			{Type: "volume", Source: "media_data", Target: "/data", Mode: "rw"},
		},
	})
	vols := obs.Project.Services["app"].Volumes
	if len(vols) != 2 {
		t.Fatalf("got %d mounts, want 2", len(vols))
	}

	var bind, vol redact.Mount
	for _, m := range vols {
		if m.Type == "bind" {
			bind = m
		} else {
			vol = m
		}
	}
	if strings.Contains(bind.Source, "/srv/media/config") {
		t.Error("bind source path was stored in cleartext")
	}
	if bind.Target != "/config" || bind.Mode != "rw" {
		t.Errorf("bind structure lost: %+v", bind)
	}
	if vol.Source != "media_data" {
		t.Errorf("named volume name = %q, want media_data (compose-generated, not a host path)", vol.Source)
	}
}
