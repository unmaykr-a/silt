package store_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

// The scheduled half of retention. Prune itself is well covered; the loop
// around it was not, and it is the part that decides how often a Raspberry Pi
// rewrites its entire database file.

func retainer(t *testing.T, db *store.Store, r *store.Retainer) {
	t.Helper()
	r.Store = db
	r.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if r.Interval == 0 && r.Live == nil {
		r.Interval = 10 * time.Millisecond
	}
}

// runUntil starts the retainer and stops it once cond holds, or fails.
func runUntil(t *testing.T, r *store.Retainer, what string, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the retainer did not stop when its context was cancelled")
	}
}

// prunable seeds one config change and one runtime-only snapshot, and returns
// the project. A retention window of a nanosecond then makes both older than
// the cutoff, which is how these tests prune against a real time.Now() without
// waiting a day.
func prunable(t *testing.T, db *store.Store, r *redact.Redactor) int64 {
	t.Helper()
	id := newProject(t, db)
	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:aaaa", startedAt: 1}))
	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:aaaa", startedAt: 2, restartCount: 1}))
	return id
}

func countSnapshots(t *testing.T, db *store.Store, id int64) int64 {
	t.Helper()
	n, err := db.RQ.CountSnapshots(context.Background(), id)
	if err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return n
}

func TestTheRetainerPrunesOnItsSchedule(t *testing.T) {
	db, r := openTestStore(t)
	id := prunable(t, db, r)
	before := countSnapshots(t, db, id)
	if before < 2 {
		t.Fatalf("the fixture stored %d snapshots, want 2", before)
	}

	ret := &store.Retainer{
		Interval: 10 * time.Millisecond,
		Policy:   store.RetentionPolicy{Unchanged: time.Nanosecond},
	}
	retainer(t, db, ret)
	runUntil(t, ret, "the runtime-only snapshot to be pruned", func() bool {
		return countSnapshots(t, db, id) < before
	})
}

func TestTheRetainerRereadsItsPolicyBetweenPasses(t *testing.T) {
	// The point of Live: a retention window changed on the settings screen
	// takes effect on the next pass, not on the next restart.
	db, r := openTestStore(t)
	id := prunable(t, db, r)
	before := countSnapshots(t, db, id)

	live := make(chan store.RetentionSettings, 1)
	current := store.RetentionSettings{Interval: 10 * time.Millisecond}
	live <- current

	ret := &store.Retainer{
		Live: func() store.RetentionSettings {
			select {
			case current = <-live:
			default:
			}
			return current
		},
	}
	retainer(t, db, ret)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = ret.Run(ctx) }()

	// Keep-forever, so several passes go by changing nothing.
	time.Sleep(60 * time.Millisecond)
	if countSnapshots(t, db, id) != before {
		cancel()
		<-done
		t.Fatal("a keep-forever policy pruned something")
	}

	live <- store.RetentionSettings{
		Interval: 10 * time.Millisecond,
		Policy:   store.RetentionPolicy{Unchanged: time.Nanosecond},
	}

	deadline := time.Now().Add(5 * time.Second)
	for countSnapshots(t, db, id) == before {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("the new policy never took effect")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestTheRetainerRunsExtraWorkOnTheSameTimer(t *testing.T) {
	// Session expiry rides along here rather than owning a second timer.
	db, _ := openTestStore(t)
	ran := make(chan struct{}, 1)
	ret := &store.Retainer{
		Interval: 10 * time.Millisecond,
		Extra: func(context.Context) {
			select {
			case ran <- struct{}{}:
			default:
			}
		},
	}
	retainer(t, db, ret)
	runUntil(t, ret, "the extra work to run", func() bool {
		select {
		case <-ran:
			return true
		default:
			return false
		}
	})
}

func TestTheVacuumCadenceSurvivesARestart(t *testing.T) {
	// The bug this exists for: lastVacuum started at the zero time, so every
	// restart vacuumed on the first pass. On a host that pulls images nightly
	// a weekly VACUUM was a nightly one, rewriting the whole file each time.
	db, _ := openTestStore(t)
	ctx := context.Background()

	first := &store.Retainer{
		Interval: 10 * time.Millisecond,
		Vacuum:   time.Hour,
	}
	retainer(t, db, first)
	runUntil(t, first, "the first vacuum to be recorded", func() bool {
		at, err := db.GetSetting(ctx, "last_vacuum_at")
		return err == nil && at != ""
	})

	recorded, err := db.GetSetting(ctx, "last_vacuum_at")
	if err != nil {
		t.Fatalf("read the recorded vacuum time: %v", err)
	}

	// A second Retainer is what a container restart looks like. An hour has
	// not passed, so it must not vacuum again.
	second := &store.Retainer{
		Interval: 10 * time.Millisecond,
		Vacuum:   time.Hour,
	}
	retainer(t, db, second)
	ctx2, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = second.Run(ctx2) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	after, err := db.GetSetting(ctx, "last_vacuum_at")
	if err != nil {
		t.Fatalf("read the vacuum time after the restart: %v", err)
	}
	if after != recorded {
		t.Errorf("a restart vacuumed again inside the cadence: %s then %s", recorded, after)
	}
}

func TestVacuumOffMeansOff(t *testing.T) {
	db, _ := openTestStore(t)
	ret := &store.Retainer{Interval: 10 * time.Millisecond, Vacuum: 0}
	retainer(t, db, ret)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = ret.Run(ctx) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if _, err := db.GetSetting(context.Background(), "last_vacuum_at"); err == nil {
		t.Error("vacuum ran with the interval set to zero")
	}
}
