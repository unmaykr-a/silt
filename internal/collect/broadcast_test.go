package collect

import (
	"testing"

	"github.com/unmaykr-a/silt/internal/store"
)

// The rule that decides whether the browser is told to look again.
//
// It used to be ConfigChanged alone, on the reasoning that a runtime change is
// already covered by the docker event that caused it. That was wrong twice
// over — the docker event is broadcast before the snapshot is written, and the
// interval sweep produces no docker event at all — and the symptom was a
// project screen that only updated on reload.
func TestShouldBroadcast(t *testing.T) {
	cases := []struct {
		name   string
		result store.SnapshotResult
		want   bool
	}{
		{
			name:   "a configuration change",
			result: store.SnapshotResult{ConfigChanged: true},
			want:   true,
		},
		{
			name: "a runtime-only change, which the project screens now show",
			// A container going unhealthy, or restarting. Before this rule it
			// was invisible until reload.
			result: store.SnapshotResult{RuntimeChanged: true},
			want:   true,
		},
		{
			name:   "an unapplied compose edit",
			result: store.SnapshotResult{FilesChanged: true},
			want:   true,
		},
		{
			name: "nothing changed at all",
			// The interval sweep on an idle host. Broadcasting here would be
			// one message per project per interval saying nothing happened.
			result: store.SnapshotResult{Touched: true},
			want:   false,
		},
		{
			name: "a touched snapshot never broadcasts, whatever else is set",
			// Touched and changed are mutually exclusive in practice; the rule
			// should not depend on that holding.
			result: store.SnapshotResult{Touched: true, ConfigChanged: true, RuntimeChanged: true},
			want:   false,
		},
		{
			name:   "an empty result",
			result: store.SnapshotResult{},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldBroadcast(tc.result); got != tc.want {
				t.Errorf("shouldBroadcast(%+v) = %v, want %v", tc.result, got, tc.want)
			}
		})
	}
}
