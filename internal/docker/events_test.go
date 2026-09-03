package docker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"

	"github.com/unmaykr-a/silt/internal/docker/dockertest"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fastBackoff keeps reconnection tests quick without disabling the ladder.
var fastBackoff = Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}

func containerEvent(action, project, service string, at time.Time) events.Message {
	return events.Message{
		Type:   events.ContainerEventType,
		Action: events.Action(action),
		Actor: events.Actor{
			ID: "container-" + service,
			Attributes: map[string]string{
				LabelProject: project,
				LabelService: service,
				"image":      "example/" + service + ":latest",
			},
		},
		TimeNano: at.UnixNano(),
	}
}

// collectEvents runs a watcher against the fake engine and records callbacks.
type recorder struct {
	mu          sync.Mutex
	events      []Event
	connects    []time.Time
	disconnects int
}

func (r *recorder) onEvent(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) onConnect(resumedFrom time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connects = append(r.connects, resumedFrom)
}

func (r *recorder) onDisconnect(error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnects++
}

func (r *recorder) snapshot() ([]Event, []time.Time, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...), append([]time.Time(nil), r.connects...), r.disconnects
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func startWatcher(t *testing.T, f *dockertest.Engine, rec *recorder) {
	t.Helper()
	c, err := New(f.Host())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	w := &Watcher{
		Client:       c,
		Log:          testLogger(),
		Backoff:      fastBackoff,
		OnEvent:      rec.onEvent,
		OnConnect:    rec.onConnect,
		OnDisconnect: rec.onDisconnect,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func TestWatcherDeliversEvents(t *testing.T) {
	f := dockertest.New()
	// Registered before the watcher so it runs after it: Close blocks until the
	// streaming /events handler returns, which only happens once the watcher's
	// context is cancelled.
	t.Cleanup(f.Close)

	rec := &recorder{}
	startWatcher(t, f, rec)

	waitFor(t, "initial connect", func() bool {
		_, connects, _ := rec.snapshot()
		return len(connects) == 1
	})

	// The first connect has nothing to resume from.
	_, connects, _ := rec.snapshot()
	if !connects[0].IsZero() {
		t.Errorf("first connect resumedFrom = %v, want zero", connects[0])
	}

	f.Emit(containerEvent("start", "media", "radarr", time.Unix(1000, 0)))
	waitFor(t, "event delivery", func() bool {
		evs, _, _ := rec.snapshot()
		return len(evs) == 1
	})

	evs, _, _ := rec.snapshot()
	got := evs[0]
	if got.Project != "media" || got.Service != "radarr" || got.Action != "start" {
		t.Errorf("event = %+v, want project=media service=radarr action=start", got)
	}
	if got.Image != "example/radarr:latest" {
		t.Errorf("image = %q, want example/radarr:latest", got.Image)
	}
}

// Healthcheck probes must never reach the rest of Silt.
func TestWatcherFiltersExecNoise(t *testing.T) {
	f := dockertest.New()
	// Registered before the watcher so it runs after it: Close blocks until the
	// streaming /events handler returns, which only happens once the watcher's
	// context is cancelled.
	t.Cleanup(f.Close)

	rec := &recorder{}
	startWatcher(t, f, rec)
	waitFor(t, "connect", func() bool {
		_, c, _ := rec.snapshot()
		return len(c) == 1
	})

	// Docker appends the command to exec actions, which is why the filter has
	// to match by prefix.
	f.Emit(containerEvent("exec_create: /healthcheck.sh", "media", "radarr", time.Unix(1001, 0)))
	f.Emit(containerEvent("exec_start: /healthcheck.sh", "media", "radarr", time.Unix(1002, 0)))
	f.Emit(containerEvent("die", "media", "radarr", time.Unix(1003, 0)))

	waitFor(t, "die event", func() bool {
		evs, _, _ := rec.snapshot()
		return len(evs) >= 1
	})
	// Give any mistakenly-passed noise a chance to arrive before asserting.
	time.Sleep(50 * time.Millisecond)

	evs, _, _ := rec.snapshot()
	if len(evs) != 1 {
		t.Fatalf("delivered %d events, want 1 (exec noise must be dropped): %+v", len(evs), evs)
	}
	if evs[0].Action != "die" {
		t.Errorf("action = %q, want die", evs[0].Action)
	}
}

// The contract that M1 exists to get right: a severed stream reconnects,
// resumes from the last event, and fires OnConnect so the caller reconciles.
func TestWatcherReconnectsAndResumes(t *testing.T) {
	f := dockertest.New()
	// Registered before the watcher so it runs after it: Close blocks until the
	// streaming /events handler returns, which only happens once the watcher's
	// context is cancelled.
	t.Cleanup(f.Close)

	rec := &recorder{}
	startWatcher(t, f, rec)

	waitFor(t, "first connect", func() bool {
		_, c, _ := rec.snapshot()
		return len(c) == 1
	})

	last := time.Unix(1700000000, 500)
	f.Emit(containerEvent("start", "media", "radarr", last))
	waitFor(t, "first event", func() bool {
		evs, _, _ := rec.snapshot()
		return len(evs) == 1
	})

	f.SeverStream()

	waitFor(t, "disconnect", func() bool {
		_, _, d := rec.snapshot()
		return d >= 1
	})
	waitFor(t, "reconnect", func() bool {
		_, c, _ := rec.snapshot()
		return len(c) >= 2
	})

	_, connects, _ := rec.snapshot()
	if connects[1].IsZero() {
		t.Error("reconnect reported resumedFrom = zero; the reconcile could not tell it was a resume")
	}
	if !connects[1].Equal(last) {
		t.Errorf("resumedFrom = %v, want %v (the last event seen)", connects[1], last)
	}

	// The resubscription must carry a since= so the daemon can replay the gap.
	waitFor(t, "resubscription", func() bool { return f.Subscriptions() >= 2 })
	since := f.SinceValues()
	if since[0] != "" {
		t.Errorf("first subscription since = %q, want empty", since[0])
	}
	if since[1] == "" {
		t.Fatal("second subscription sent no since=; the replay gap would be lost")
	}

	// Events still flow on the new connection.
	f.Emit(containerEvent("die", "media", "radarr", last.Add(time.Second)))
	waitFor(t, "post-reconnect event", func() bool {
		evs, _, _ := rec.snapshot()
		return len(evs) == 2
	})
}

// An engine that is down at startup must not be fatal; the watcher keeps
// retrying and connects when it returns.
func TestWatcherRetriesUnreachableEngine(t *testing.T) {
	f := dockertest.New()
	// Registered before the watcher so it runs after it: Close blocks until the
	// streaming /events handler returns, which only happens once the watcher's
	// context is cancelled.
	t.Cleanup(f.Close)
	f.SetReachable(false)

	rec := &recorder{}
	startWatcher(t, f, rec)

	time.Sleep(50 * time.Millisecond)
	if _, connects, _ := rec.snapshot(); len(connects) != 0 {
		t.Fatalf("connected %d times while engine was down, want 0", len(connects))
	}

	f.SetReachable(true)
	waitFor(t, "connect after recovery", func() bool {
		_, c, _ := rec.snapshot()
		return len(c) >= 1
	})
}
