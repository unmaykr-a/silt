package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Live probes ask rather than infer. The failure they exist for is the one
// that looks identical to a working install from every other screen: a compose
// root that was configured and never mounted renders exactly like a project
// with no files.

type probesPayload struct {
	CheckedAt int64 `json:"checked_at"`
	Probes    []struct {
		ID     string `json:"id"`
		Label  string `json:"label"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
		TookMS int64  `json:"took_ms"`
	} `json:"probes"`
}

func probes(t *testing.T, f *fixture) probesPayload {
	t.Helper()
	resp, body := f.get(t, "/api/settings/probes")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/settings/probes = %d: %s", resp.StatusCode, body)
	}
	var out probesPayload
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func find(t *testing.T, p probesPayload, id string) (bool, string) {
	t.Helper()
	for _, probe := range p.Probes {
		if probe.ID == id {
			return probe.OK, probe.Detail
		}
	}
	var ids []string
	for _, probe := range p.Probes {
		ids = append(ids, probe.ID)
	}
	t.Fatalf("no probe %q; got %v", id, ids)
	return false, ""
}

func TestTheDatabaseProbeAnswers(t *testing.T) {
	f := newFixture(t)
	ok, detail := find(t, probes(t, f), "database")
	if !ok {
		t.Errorf("the database probe failed against a live fixture: %s", detail)
	}
}

func TestAnUnreachableDockerEndpointIsReportedRatherThanInferred(t *testing.T) {
	// No prober is wired in the fixture, which is the same shape as an engine
	// that will not answer: the probe says so instead of the screen quietly
	// showing a host with nothing on it.
	ok, detail := find(t, probes(t, newFixture(t)), "docker")
	if ok {
		t.Errorf("docker reported healthy with no client wired: %s", detail)
	}
	if detail == "" {
		t.Error("a failed probe gave no reason")
	}
}

func TestTheDockerProbeReportsAWorkingEngine(t *testing.T) {
	f := newFixture(t)
	f.api.SetProber(fakeProber{version: "28.1.0"})

	ok, detail := find(t, probes(t, f), "docker")
	if !ok {
		t.Fatalf("docker probe failed: %s", detail)
	}
	if !strings.Contains(detail, "28.1.0") {
		t.Errorf("detail does not name the version: %s", detail)
	}
}

func TestTheDockerProbeReportsTheError(t *testing.T) {
	f := newFixture(t)
	f.api.SetProber(fakeProber{err: fmt.Errorf("connection refused")})

	ok, detail := find(t, probes(t, f), "docker")
	if ok {
		t.Fatal("a refusing engine reported healthy")
	}
	if !strings.Contains(detail, "connection refused") {
		t.Errorf("detail does not carry the error: %s", detail)
	}
}

func TestAComposeRootThatIsNotThereSaysSo(t *testing.T) {
	f := newFixtureWithRoots(t, []string{"/definitely/not/mounted"})
	ok, detail := find(t, probes(t, f), "compose:/definitely/not/mounted")
	if ok {
		t.Fatal("a missing compose root reported healthy")
	}
	// The overwhelmingly common cause, and the one the raw error does not
	// suggest to anyone reading it in a container.
	if !strings.Contains(detail, "mounted") {
		t.Errorf("detail does not mention mounting: %s", detail)
	}
}

func TestAComposeRootThatIsThereIsReadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f := newFixtureWithRoots(t, []string{dir})

	ok, detail := find(t, probes(t, f), "compose:"+dir)
	if !ok {
		t.Fatalf("a readable root failed: %s", detail)
	}
	if !strings.Contains(detail, "1 entries") {
		t.Errorf("detail does not report what is in it: %s", detail)
	}
}

func TestNoComposeRootsIsNotAFailure(t *testing.T) {
	// Not configuring file capture is a choice, not a fault. Reporting it red
	// would train people to ignore the panel.
	ok, detail := find(t, probes(t, newFixture(t)), "compose")
	if !ok {
		t.Errorf("no compose roots reported as a failure: %s", detail)
	}
}

type fakeProber struct {
	version string
	err     error
}

func (f fakeProber) DockerVersion(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.version, nil
}
