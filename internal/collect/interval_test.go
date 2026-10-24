package collect_test

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/collect"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// The reconcile cadence is editable from the settings screen, and this loop
// read it once.
//
// A cadence of zero parked the goroutine on ctx.Done(), so turning periodic
// reconciles on never started one until the container was restarted; and the
// re-read only accepted a positive value, so turning them off left the ticker
// running at the old cadence. Both directions were a setting that reported
// itself saved and did nothing, which is worse than a setting that is not
// offered.

// observations is how many times the project has been seen. A reconcile that
// finds nothing changed does not insert a snapshot row -- it bumps the count on
// the existing one -- so this is the signal that a pass actually ran, and
// counting rows would see nothing.
func observations(t *testing.T, db *store.Store) int64 {
	t.Helper()
	snaps, err := db.RQ.SnapshotsInRange(t.Context(), sqlcgen.SnapshotsInRangeParams{
		FromTs: 0, ToTs: store.Now() + time.Hour.Milliseconds(), MaxRows: 1000,
	})
	if err != nil {
		t.Fatalf("read snapshots: %v", err)
	}
	var total int64
	for _, s := range snaps {
		total += s.ObservationCount
	}
	return total
}

func waitForGrowth(t *testing.T, db *store.Store, from int64, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if observations(t, db) > from {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// settle waits for the connect-time reconcile to finish, so a later count is
// measuring the interval loop rather than the stream connecting.
func settle(t *testing.T, db *store.Store) int64 {
	t.Helper()
	if !waitForGrowth(t, db, 0, 3*time.Second) {
		t.Fatal("the connect-time reconcile never ran")
	}
	var last int64
	for i := 0; i < 50; i++ {
		now := observations(t, db)
		if now == last && now > 0 {
			return now
		}
		last = now
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

func intervalCollector(h *harness, fn func() time.Duration) *collect.Collector {
	c := collector(h)
	c.IntervalFn = fn
	c.Interval = 0
	// Short enough for a test to wait on; a minute in production.
	c.IdlePoll = 25 * time.Millisecond
	c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return c
}

func TestTheReconcileCadenceCanBeTurnedOnWhileRunning(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "lscr.io/linuxserver/radarr:latest", "sha256:aaaa", nil, nil)

	var cadence atomic.Int64 // nanoseconds; starts disabled
	c := intervalCollector(h, func() time.Duration { return time.Duration(cadence.Load()) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the collector did not stop when its context was cancelled")
		}
	}()

	baseline := settle(t, h.db)

	// Off: nothing should accumulate beyond the connect-time pass.
	time.Sleep(200 * time.Millisecond)
	if got := observations(t, h.db); got != baseline {
		t.Fatalf("observations grew from %d to %d while the cadence was off", baseline, got)
	}

	// On, the way a save from the settings screen turns it on.
	cadence.Store(int64(30 * time.Millisecond))
	if !waitForGrowth(t, h.db, baseline, 3*time.Second) {
		t.Error("turning the reconcile cadence on did not start it; it needed a restart")
	}
}

func TestTheReconcileCadenceCanBeTurnedOffWhileRunning(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "lscr.io/linuxserver/radarr:latest", "sha256:aaaa", nil, nil)

	var cadence atomic.Int64
	cadence.Store(int64(30 * time.Millisecond))
	c := intervalCollector(h, func() time.Duration { return time.Duration(cadence.Load()) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the collector did not stop when its context was cancelled")
		}
	}()

	if !waitForGrowth(t, h.db, 0, 3*time.Second) {
		t.Fatal("the cadence never ran while it was on")
	}

	cadence.Store(0)
	// One cadence plus one idle poll is enough for the loop to notice; wait
	// longer than that, then take the count and prove it stops moving.
	time.Sleep(300 * time.Millisecond)
	stopped := observations(t, h.db)
	time.Sleep(300 * time.Millisecond)
	if got := observations(t, h.db); got != stopped {
		t.Errorf("observations grew from %d to %d after the cadence was turned off", stopped, got)
	}
}
