package collect

import (
	"testing"

	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/store"
)

// The severity a Docker event lands with decides whether it reaches the error
// count on the timeline, and whether a notification filter set to "medium and
// above" lets it through. Getting it wrong is silent in both directions.
func TestDockerEventSeverity(t *testing.T) {
	cases := []struct {
		action string
		want   string
	}{
		// Something went wrong on its own.
		{"die", store.SeverityError},
		{"oom", store.SeverityError},
		{"kill", store.SeverityError},
		{"health_status: unhealthy", store.SeverityError},

		// Somebody did something. Worth seeing, not an alarm.
		{"stop", store.SeverityWarn},
		{"restart", store.SeverityWarn},
		{"pause", store.SeverityWarn},

		// Routine.
		{"start", store.SeverityInfo},
		{"create", store.SeverityInfo},
		{"health_status: healthy", store.SeverityInfo},
		{"unpause", store.SeverityInfo},
		// An action a future Docker adds must not be an error by default, or
		// every upgrade of the engine turns the timeline red.
		{"some_future_action", store.SeverityInfo},
		{"", store.SeverityInfo},
	}

	for _, tc := range cases {
		if got := dockerEventSeverity(tc.action); got != tc.want {
			t.Errorf("dockerEventSeverity(%q) = %q, want %q", tc.action, got, tc.want)
		}
	}
}

func TestBatchActionsAreDistinctAndOrdered(t *testing.T) {
	// One `compose up` fires the same actions across several containers; the
	// summary should read "create, start", not the raw dozen.
	b := Batch{Events: []docker.Event{
		{Action: "create"}, {Action: "start"}, {Action: "create"},
		{Action: "start"}, {Action: "die"},
	}}
	got := b.Actions()
	want := []string{"create", "start", "die"}
	if len(got) != len(want) {
		t.Fatalf("Actions() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Actions()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBatchServicesSkipsBlanks(t *testing.T) {
	// Not every Docker event carries a Compose service label — a volume or
	// network event does not — and an empty name in the summary reads as a
	// service called nothing.
	b := Batch{Events: []docker.Event{
		{Service: "radarr"}, {Service: ""}, {Service: "sonarr"},
		{Service: "radarr"}, {Service: ""},
	}}
	got := b.Services()
	if len(got) != 2 || got[0] != "radarr" || got[1] != "sonarr" {
		t.Errorf("Services() = %v, want [radarr sonarr]", got)
	}
}

func TestBatchAccessorsOnAnEmptyBatch(t *testing.T) {
	var b Batch
	if len(b.Actions()) != 0 || len(b.Services()) != 0 {
		t.Errorf("an empty batch produced %v / %v", b.Actions(), b.Services())
	}
}
