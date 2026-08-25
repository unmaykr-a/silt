package diff_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/diff"
	"github.com/unmaykr-a/silt/internal/redact"
)

// svc returns a fully populated service, so each test can change exactly one
// field and assert that exactly one kind is reported.
func svc() compose.Service {
	return compose.Service{
		Image:         "lscr.io/linuxserver/radarr:latest",
		ImageID:       "sha256:aaaa",
		ImageDigest:   "sha256:1111",
		Environment:   map[string]string{"PUID": "1000", "API_KEY": "[redacted:aaaabbbbcccc]"},
		Command:       []string{"/init"},
		Entrypoint:    []string{"/entry.sh"},
		Labels:        map[string]string{"com.docker.compose.service": "radarr"},
		Ports:         []string{"0.0.0.0:7878->7878/tcp"},
		ExposedPorts:  []string{"7878/tcp"},
		Networks:      []string{"media_default"},
		DependsOn:     []string{"db"},
		RestartPolicy: "unless-stopped",
		Healthcheck:   []string{"CMD", "curl", "-f", "http://localhost:7878"},
		MemoryLimit:   1 << 30,
		Volumes: []redact.Mount{
			{Type: "bind", Source: "[redacted:aaaabbbbcccc]", Target: "/config", Mode: "rw"},
		},
	}
}

func input(id int64, services map[string]compose.Service, runtimes map[string]diff.Runtime) diff.Input {
	return diff.Input{
		Side:     diff.Side{SnapshotID: id, TakenAt: id * 1000},
		Project:  compose.Project{Name: "media", Source: compose.SourceContainers, Services: services},
		Runtimes: runtimes,
	}
}

func one(name string, s compose.Service) map[string]compose.Service {
	return map[string]compose.Service{name: s}
}

// mutate applies fn to a copy of the baseline service.
func mutate(fn func(*compose.Service)) compose.Service {
	s := svc()
	fn(&s)
	return s
}

// Every change kind, one field at a time. A change that reports the wrong kind
// lands in the wrong notification filter and the wrong UI colour, so the kind
// matters as much as detecting the change at all.
func TestChangeKinds(t *testing.T) {
	tests := []struct {
		name         string
		after        compose.Service
		wantKind     diff.Kind
		wantSeverity diff.Severity
		wantOp       diff.Op
	}{
		{"image ref retagged", mutate(func(s *compose.Service) { s.Image = "lscr.io/linuxserver/radarr:5.0" }), diff.KindImageRef, diff.Low, diff.OpReplace},
		{"image id changed", mutate(func(s *compose.Service) { s.ImageID = "sha256:bbbb" }), diff.KindImageID, diff.High, diff.OpReplace},
		{"image digest changed", mutate(func(s *compose.Service) { s.ImageDigest = "sha256:2222" }), diff.KindImageDigest, diff.High, diff.OpReplace},
		{"env value changed", mutate(func(s *compose.Service) { s.Environment["PUID"] = "1001" }), diff.KindEnv, diff.Medium, diff.OpReplace},
		{"env key added", mutate(func(s *compose.Service) { s.Environment["NEW"] = "x" }), diff.KindEnv, diff.Medium, diff.OpAdd},
		{"env key removed", mutate(func(s *compose.Service) { delete(s.Environment, "PUID") }), diff.KindEnv, diff.Medium, diff.OpRemove},
		{"port added", mutate(func(s *compose.Service) { s.Ports = append(s.Ports, "0.0.0.0:9999->9999/tcp") }), diff.KindPorts, diff.Medium, diff.OpAdd},
		{"volume changed", mutate(func(s *compose.Service) {
			s.Volumes = []redact.Mount{{Type: "bind", Source: "[redacted:ddddeeeeffff]", Target: "/config", Mode: "rw"}}
		}), diff.KindVolumes, diff.High, diff.OpReplace},
		{"network added", mutate(func(s *compose.Service) { s.Networks = append(s.Networks, "proxy") }), diff.KindNetworks, diff.Medium, diff.OpAdd},
		{"healthcheck changed", mutate(func(s *compose.Service) { s.Healthcheck = []string{"CMD", "true"} }), diff.KindHealthcheck, diff.Medium, diff.OpReplace},
		{"memory limit changed", mutate(func(s *compose.Service) { s.MemoryLimit = 2 << 30 }), diff.KindResources, diff.Low, diff.OpReplace},
		{"command changed", mutate(func(s *compose.Service) { s.Command = []string{"/init", "--verbose"} }), diff.KindCommand, diff.Medium, diff.OpReplace},
		{"entrypoint changed", mutate(func(s *compose.Service) { s.Entrypoint = []string{"/other.sh"} }), diff.KindEntrypoint, diff.Medium, diff.OpReplace},
		{"restart policy changed", mutate(func(s *compose.Service) { s.RestartPolicy = "always" }), diff.KindRestartPolicy, diff.Low, diff.OpReplace},
		{"label changed", mutate(func(s *compose.Service) { s.Labels["traefik.enable"] = "true" }), diff.KindLabels, diff.Low, diff.OpAdd},
		{"depends_on changed", mutate(func(s *compose.Service) { s.DependsOn = []string{"db", "cache"} }), diff.KindDependsOn, diff.Medium, diff.OpAdd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := diff.Compute(
				input(1, one("radarr", svc()), nil),
				input(2, one("radarr", tt.after), nil),
			)
			if len(res.Changes) != 1 {
				t.Fatalf("got %d changes, want exactly 1: %+v", len(res.Changes), res.Changes)
			}
			c := res.Changes[0]
			if c.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", c.Kind, tt.wantKind)
			}
			if c.Severity != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", c.Severity, tt.wantSeverity)
			}
			if c.Op != tt.wantOp {
				t.Errorf("op = %q, want %q", c.Op, tt.wantOp)
			}
			if c.Service != "radarr" {
				t.Errorf("service = %q, want radarr", c.Service)
			}
			if res.Summary[tt.wantKind] != 1 {
				t.Errorf("summary[%s] = %d, want 1", tt.wantKind, res.Summary[tt.wantKind])
			}
		})
	}
}

func TestServiceAddedAndRemoved(t *testing.T) {
	empty := map[string]compose.Service{}

	added := diff.Compute(input(1, empty, nil), input(2, one("radarr", svc()), nil))
	if len(added.Changes) != 1 || added.Changes[0].Kind != diff.KindServiceAdded {
		t.Fatalf("added: got %+v", added.Changes)
	}
	if added.Changes[0].Op != diff.OpAdd || added.Changes[0].After == "" {
		t.Errorf("added change should carry the new image: %+v", added.Changes[0])
	}

	removed := diff.Compute(input(1, one("radarr", svc()), nil), input(2, empty, nil))
	if len(removed.Changes) != 1 || removed.Changes[0].Kind != diff.KindServiceRemove {
		t.Fatalf("removed: got %+v", removed.Changes)
	}
	if removed.Changes[0].Severity != diff.High {
		t.Errorf("service_removed severity = %q, want high", removed.Changes[0].Severity)
	}
}

// Identical observations must produce nothing. Anything else makes the UI
// cry wolf on every interval snapshot.
func TestIdenticalProducesNoChanges(t *testing.T) {
	res := diff.Compute(input(1, one("radarr", svc()), nil), input(2, one("radarr", svc()), nil))
	if len(res.Changes) != 0 {
		t.Errorf("identical inputs produced %d changes: %+v", len(res.Changes), res.Changes)
	}
	if len(res.Summary) != 0 {
		t.Errorf("summary should be empty, got %v", res.Summary)
	}
}

// Reordering a set-valued field is not a change. This is what normalisation
// buys, and the diff must not undo it.
func TestReorderingSetsIsNotAChange(t *testing.T) {
	before := mutate(func(s *compose.Service) {
		s.Networks = []string{"a", "b", "c"}
		s.Ports = []string{"1->1/tcp", "2->2/tcp"}
	})
	after := mutate(func(s *compose.Service) {
		s.Networks = []string{"c", "a", "b"}
		s.Ports = []string{"2->2/tcp", "1->1/tcp"}
	})

	res := diff.Compute(input(1, one("radarr", before), nil), input(2, one("radarr", after), nil))
	if len(res.Changes) != 0 {
		t.Errorf("reordering produced %d changes: %+v", len(res.Changes), res.Changes)
	}
}

// Command order IS meaningful and must be reported.
func TestReorderingCommandIsAChange(t *testing.T) {
	before := mutate(func(s *compose.Service) { s.Command = []string{"serve", "--fast"} })
	after := mutate(func(s *compose.Service) { s.Command = []string{"--fast", "serve"} })

	res := diff.Compute(input(1, one("radarr", before), nil), input(2, one("radarr", after), nil))
	if len(res.Changes) != 1 || res.Changes[0].Kind != diff.KindCommand {
		t.Errorf("command reorder should be reported: %+v", res.Changes)
	}
}

// The whole point of redaction surviving into the diff: report that a secret
// changed without ever holding either value.
func TestRedactedValuesDiffWithoutRevealing(t *testing.T) {
	before := mutate(func(s *compose.Service) { s.Environment["API_KEY"] = "[redacted:aaaabbbbcccc]" })
	after := mutate(func(s *compose.Service) { s.Environment["API_KEY"] = "[redacted:ddddeeeeffff]" })

	res := diff.Compute(input(1, one("radarr", before), nil), input(2, one("radarr", after), nil))
	if len(res.Changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(res.Changes), res.Changes)
	}
	c := res.Changes[0]
	if c.Kind != diff.KindEnv || c.Path != "services.radarr.environment.API_KEY" {
		t.Errorf("unexpected change: %+v", c)
	}
	if !strings.HasPrefix(c.Before, "[redacted:") || !strings.HasPrefix(c.After, "[redacted:") {
		t.Errorf("diff exposed raw values: before=%q after=%q", c.Before, c.After)
	}
}

// Privileged is a security-relevant change, not a footnote.
func TestGainingPrivilegedIsHighSeverity(t *testing.T) {
	after := mutate(func(s *compose.Service) { s.Privileged = true })
	res := diff.Compute(input(1, one("radarr", svc()), nil), input(2, one("radarr", after), nil))

	if len(res.Changes) != 1 {
		t.Fatalf("got %d changes: %+v", len(res.Changes), res.Changes)
	}
	if res.Changes[0].Severity != diff.High {
		t.Errorf("severity = %q, want high", res.Changes[0].Severity)
	}
}

// Runtime state is diffed from the runtime map, never from the project model.
func TestRuntimeStateChanges(t *testing.T) {
	before := map[string]diff.Runtime{"radarr": {State: "running", Health: "healthy", RestartCount: 0}}
	after := map[string]diff.Runtime{"radarr": {State: "restarting", Health: "unhealthy", RestartCount: 3}}

	res := diff.Compute(
		input(1, one("radarr", svc()), before),
		input(2, one("radarr", svc()), after),
	)
	if len(res.Changes) != 3 {
		t.Fatalf("got %d changes, want state, health and restart_count: %+v", len(res.Changes), res.Changes)
	}
	for _, c := range res.Changes {
		if c.Kind != diff.KindState {
			t.Errorf("change %+v should be kind state", c)
		}
	}
}

// A config change and a runtime change in the same pair must both appear, and
// must not be conflated.
func TestConfigAndRuntimeChangesCoexist(t *testing.T) {
	after := mutate(func(s *compose.Service) { s.ImageID = "sha256:bbbb" })
	res := diff.Compute(
		input(1, one("radarr", svc()), map[string]diff.Runtime{"radarr": {State: "running"}}),
		input(2, one("radarr", after), map[string]diff.Runtime{"radarr": {State: "restarting"}}),
	)
	if res.Summary[diff.KindImageID] != 1 {
		t.Errorf("missing image_id change: %+v", res.Changes)
	}
	if res.Summary[diff.KindState] != 1 {
		t.Errorf("missing state change: %+v", res.Changes)
	}
}

// Output order must be stable so the same pair always renders identically.
func TestOutputIsDeterministic(t *testing.T) {
	after := mutate(func(s *compose.Service) {
		s.ImageID = "sha256:bbbb"
		s.Environment["PUID"] = "1001"
		s.Networks = append(s.Networks, "proxy")
		s.Labels["a"] = "1"
	})
	services := map[string]compose.Service{"radarr": svc(), "sonarr": svc()}
	changed := map[string]compose.Service{"radarr": after, "sonarr": after}

	first, err := json.Marshal(diff.Compute(input(1, services, nil), input(2, changed, nil)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := json.Marshal(diff.Compute(input(1, services, nil), input(2, changed, nil)))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("diff output varied on round %d", i)
		}
	}
}

// Multiple services must be grouped and ordered by service name.
func TestChangesAreOrderedByService(t *testing.T) {
	after := mutate(func(s *compose.Service) { s.ImageID = "sha256:bbbb" })
	res := diff.Compute(
		input(1, map[string]compose.Service{"zulu": svc(), "alpha": svc()}, nil),
		input(2, map[string]compose.Service{"zulu": after, "alpha": after}, nil),
	)
	if len(res.Changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(res.Changes))
	}
	if res.Changes[0].Service != "alpha" || res.Changes[1].Service != "zulu" {
		t.Errorf("changes not ordered by service: %q, %q", res.Changes[0].Service, res.Changes[1].Service)
	}
}
