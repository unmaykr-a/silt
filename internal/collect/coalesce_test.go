package collect

import (
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/docker"
)

const testWindow = 60 * time.Millisecond

func ev(project, service, action string, at time.Time) docker.Event {
	return docker.Event{
		Type:    "container",
		Action:  action,
		Project: project,
		Service: service,
		At:      at,
	}
}

func recv(t *testing.T, c *Coalescer) Batch {
	t.Helper()
	select {
	case b, ok := <-c.C():
		if !ok {
			t.Fatal("coalescer channel closed while awaiting a batch")
		}
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a batch")
		return Batch{}
	}
}

// A `docker compose up` fires a burst; it must produce exactly one batch.
func TestCoalescesBurstIntoOneBatch(t *testing.T) {
	c := NewCoalescer(testWindow)
	defer c.Close()

	base := time.Unix(1700000000, 0)
	for i, action := range []string{"create", "start", "start", "die", "start"} {
		c.Add(ev("media", "radarr", action, base.Add(time.Duration(i)*time.Millisecond)))
	}

	b := recv(t, c)
	if b.Project != "media" {
		t.Errorf("project = %q, want media", b.Project)
	}
	if len(b.Events) != 5 {
		t.Errorf("events = %d, want 5", len(b.Events))
	}
	want := []string{"create", "start", "die"}
	if got := b.Actions(); len(got) != len(want) {
		t.Errorf("distinct actions = %v, want %v", got, want)
	}
}

// Separate projects must not be merged into one batch.
func TestSeparatesProjects(t *testing.T) {
	c := NewCoalescer(testWindow)
	defer c.Close()

	base := time.Unix(1700000000, 0)
	c.Add(ev("media", "radarr", "start", base))
	c.Add(ev("tools", "vaultwarden", "start", base))

	seen := map[string]int{}
	for i := 0; i < 2; i++ {
		b := recv(t, c)
		seen[b.Project] = len(b.Events)
	}
	if seen["media"] != 1 || seen["tools"] != 1 {
		t.Errorf("batches = %v, want one event for each of media and tools", seen)
	}
}

// The window opens on the first event and closes on a fixed deadline, so a
// steady trickle cannot hold it open forever.
func TestWindowIsNotExtendedByLateEvents(t *testing.T) {
	c := NewCoalescer(testWindow)
	defer c.Close()

	base := time.Unix(1700000000, 0)
	c.Add(ev("media", "radarr", "start", base))

	// Keep feeding events for longer than the window; the first batch must
	// still close on schedule rather than being deferred indefinitely.
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				i++
				c.Add(ev("media", "radarr", "start", base.Add(time.Duration(i)*time.Millisecond)))
			}
		}
	}()
	defer close(stop)

	start := time.Now()
	recv(t, c)
	if elapsed := time.Since(start); elapsed > 4*testWindow {
		t.Errorf("batch took %v to close under a steady trickle; window was extended", elapsed)
	}
}

// A second burst after the window closes is a separate batch.
func TestSecondBurstIsANewBatch(t *testing.T) {
	c := NewCoalescer(testWindow)
	defer c.Close()

	base := time.Unix(1700000000, 0)
	c.Add(ev("media", "radarr", "start", base))
	first := recv(t, c)
	if len(first.Events) != 1 {
		t.Fatalf("first batch had %d events, want 1", len(first.Events))
	}

	c.Add(ev("media", "radarr", "die", base.Add(time.Second)))
	second := recv(t, c)
	if len(second.Events) != 1 || second.Events[0].Action != "die" {
		t.Errorf("second batch = %+v, want a single die event", second.Events)
	}
}

// Events with no project label cannot be attributed and are dropped here.
func TestDropsEventsWithoutProject(t *testing.T) {
	c := NewCoalescer(testWindow)
	defer c.Close()

	c.Add(ev("", "", "start", time.Unix(1700000000, 0)))

	select {
	case b := <-c.C():
		t.Fatalf("got batch %+v, want none", b)
	case <-time.After(3 * testWindow):
	}
}

func TestBatchServicesAreDistinctAndOrdered(t *testing.T) {
	base := time.Unix(1700000000, 0)
	b := Batch{Events: []docker.Event{
		ev("media", "radarr", "start", base),
		ev("media", "sonarr", "start", base),
		ev("media", "radarr", "die", base),
		ev("media", "", "pull", base),
	}}
	got := b.Services()
	want := []string{"radarr", "sonarr"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Services() = %v, want %v", got, want)
	}
}

// Close must not hang, even with a window still open and nobody reading.
func TestCloseWithPendingBatchDoesNotHang(t *testing.T) {
	c := NewCoalescer(10 * time.Second)
	c.Add(ev("media", "radarr", "start", time.Unix(1700000000, 0)))

	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung with a pending window and no reader")
	}

	if _, ok := <-c.C(); ok {
		t.Error("channel yielded a batch after Close")
	}
}

// Close is idempotent: the collector defers it and also calls it explicitly.
func TestCloseIsIdempotent(t *testing.T) {
	c := NewCoalescer(testWindow)
	c.Close()
	c.Close()
	c.Add(ev("media", "radarr", "start", time.Unix(1700000000, 0)))
}
