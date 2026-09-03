package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// normaliseInspect is where every runtime fact the UI shows is read out of
// Docker's response: state, health, restarts, and — since 0.8.0 — why a
// container stopped. It was the least-tested pure function in the project and
// the one carrying the most recently added logic.

func inspected(state *container.State) Inspected {
	return normaliseInspect(container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID: "abc123", Name: "/media-radarr-1", State: state, RestartCount: 3,
		},
		Config: &container.Config{Image: "radarr:5.4.0", Env: []string{"TZ=Europe/Tallinn"}},
	})
}

func TestNormaliseReadsRuntimeState(t *testing.T) {
	got := inspected(&container.State{
		Status:    "running",
		Health:    &container.Health{Status: "healthy"},
		StartedAt: "2026-09-01T10:00:00.000000000Z",
	})

	if got.Runtime.State != "running" {
		t.Errorf("State = %q, want running", got.Runtime.State)
	}
	if got.Runtime.Health != "healthy" {
		t.Errorf("Health = %q, want healthy", got.Runtime.Health)
	}
	if got.Runtime.RestartCount != 3 {
		t.Errorf("RestartCount = %d, want 3", got.Runtime.RestartCount)
	}
	if got.Runtime.ContainerName != "media-radarr-1" {
		t.Errorf("ContainerName = %q; the leading slash should be gone", got.Runtime.ContainerName)
	}
	if got.Runtime.StartedAt == nil {
		t.Fatal("StartedAt is nil for a container with a start time")
	}
	want := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	if *got.Runtime.StartedAt != want {
		t.Errorf("StartedAt = %d, want %d", *got.Runtime.StartedAt, want)
	}
}

// A container with no healthcheck reports no health. That is not the same as
// healthy, and inventing a value here would make the whole state vocabulary
// lie about every image that ships without one.
func TestNormaliseLeavesHealthEmptyWithoutAHealthcheck(t *testing.T) {
	got := inspected(&container.State{Status: "running"})
	if got.Runtime.Health != "" {
		t.Errorf("Health = %q, want empty for a container with no healthcheck", got.Runtime.Health)
	}
}

// The exit code is only meaningful once a container has stopped. Docker keeps
// reporting the previous run's code while one is up, and a stale 0 presented
// as current state reads as "exited cleanly" about something running fine.
func TestNormaliseOnlyReadsAnExitCodeFromAStoppedContainer(t *testing.T) {
	for _, status := range []string{"running", "restarting", "paused", "created"} {
		got := inspected(&container.State{Status: status, ExitCode: 137, OOMKilled: true})
		if got.Runtime.ExitCode != nil {
			t.Errorf("%s: ExitCode = %d, want none — it is the previous run's",
				status, *got.Runtime.ExitCode)
		}
		if got.Runtime.OOMKilled {
			t.Errorf("%s: OOMKilled set for a container that has not stopped", status)
		}
	}
}

func TestNormaliseReadsWhyAContainerStopped(t *testing.T) {
	cases := []struct {
		status string
		code   int
		oom    bool
	}{
		{"exited", 0, false},   // someone stopped it
		{"exited", 1, false},   // it died
		{"exited", 137, true},  // the kernel killed it for memory
		{"exited", 137, false}, // a plain docker kill — same code, different problem
		{"dead", 255, false},
	}
	for _, tc := range cases {
		got := inspected(&container.State{Status: tc.status, ExitCode: tc.code, OOMKilled: tc.oom})
		if got.Runtime.ExitCode == nil {
			t.Fatalf("%s/%d: no exit code recorded for a stopped container", tc.status, tc.code)
		}
		if *got.Runtime.ExitCode != tc.code {
			t.Errorf("%s: ExitCode = %d, want %d", tc.status, *got.Runtime.ExitCode, tc.code)
		}
		if got.Runtime.OOMKilled != tc.oom {
			t.Errorf("%s/%d: OOMKilled = %v, want %v", tc.status, tc.code, got.Runtime.OOMKilled, tc.oom)
		}
	}
}

// Exit code 0 must survive as zero rather than becoming "not set". The whole
// point of the pointer is that "stopped cleanly" and "did not stop" are
// different answers.
func TestNormaliseKeepsAZeroExitCodeDistinctFromNone(t *testing.T) {
	stopped := inspected(&container.State{Status: "exited", ExitCode: 0})
	if stopped.Runtime.ExitCode == nil {
		t.Fatal("a clean stop was recorded as no exit code at all")
	}
	if *stopped.Runtime.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", *stopped.Runtime.ExitCode)
	}
	running := inspected(&container.State{Status: "running"})
	if running.Runtime.ExitCode != nil {
		t.Error("a running container reported an exit code")
	}
}

// An unparseable or zero start time must leave StartedAt unset rather than
// landing at the Unix epoch, which would render as "56 years ago".
func TestNormaliseIgnoresAnUnusableStartTime(t *testing.T) {
	for _, raw := range []string{"", "0001-01-01T00:00:00Z", "not a time"} {
		got := inspected(&container.State{Status: "running", StartedAt: raw})
		if got.Runtime.StartedAt != nil {
			t.Errorf("StartedAt=%q produced %d, want nil", raw, *got.Runtime.StartedAt)
		}
	}
}

// A response with no State section at all should not panic.
func TestNormaliseSurvivesAMissingState(t *testing.T) {
	got := inspected(nil)
	if got.Runtime.State != "" || got.Runtime.ExitCode != nil {
		t.Errorf("a response with no state produced %+v", got.Runtime)
	}
	if got.Runtime.ContainerID != "abc123" {
		t.Errorf("the identity should still be read: %+v", got.Runtime)
	}
}
