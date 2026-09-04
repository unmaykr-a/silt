package collect_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"

	"github.com/unmaykr-a/silt/internal/collect"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/docker/dockertest"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// The event-driven half of the collector: a Docker event arriving, becoming a
// stored event, reaching the browser, and — through the coalescer — producing
// a snapshot. Every piece of this was uncovered, and it is the path that runs
// on every `compose up` anyone ever does.

func collector(h *harness) *collect.Collector {
	return &collect.Collector{
		Client:      h.client,
		Snapshotter: h.snap,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Short enough that a test does not wait on it, long enough that the
		// events in one test still coalesce into a single batch.
		Window:   50 * time.Millisecond,
		Interval: time.Hour,
	}
}

// runFor starts the collector, lets the engine deliver, then stops it.
func runFor(t *testing.T, c *collect.Collector, during func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	during()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the collector did not stop when its context was cancelled")
	}
}

// hasEvent reports whether an event of this type has landed. Waiting on "any
// event" is a trap: the connect-time reconcile records snapshot.changed first,
// so a test that stopped there would cancel the collector before the event it
// was actually waiting for had been processed.
func hasEvent(t *testing.T, db *store.Store, typ string) bool {
	t.Helper()
	for _, row := range storedEvents(t, db) {
		if row.Type == typ {
			return true
		}
	}
	return false
}

func storedEvents(t *testing.T, db *store.Store) []sqlcgen.Event {
	t.Helper()
	rows, err := db.RQ.ListEvents(context.Background(), sqlcgen.ListEventsParams{
		FromTs: 0, ToTs: store.Now() + time.Hour.Milliseconds(), MaxRows: 100,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return rows
}

// waitFor polls until cond holds, so a test never sleeps for a fixed time it
// has to guess at.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestADockerEventIsRecordedAndBroadcast(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaa", nil, nil)

	runFor(t, collector(h), func() {
		waitFor(t, "the watcher to subscribe", func() bool { return h.engine.Subscriptions() > 0 })
		h.engine.Emit(containerEvent("die", "media", "radarr"))
		waitFor(t, "the die event to be stored", func() bool {
			return hasEvent(t, h.db, "container.die")
		})
	})

	rows := storedEvents(t, h.db)
	var die sqlcgen.Event
	for _, row := range rows {
		if row.Type == "container.die" {
			die = row
		}
	}
	if die.Type == "" {
		t.Fatalf("no container.die event; got %d events", len(rows))
	}
	// A container dying is an error, not a note: it is what the timeline's
	// severity colouring and the notification filter both key off.
	if die.Severity != store.SeverityError {
		t.Errorf("severity = %s, want %s", die.Severity, store.SeverityError)
	}
	if die.Service != "radarr" {
		t.Errorf("service = %q, want radarr", die.Service)
	}
	// Linked to the project, which is what puts it on that project's timeline
	// rather than only the global one.
	if !die.ProjectID.Valid {
		t.Error("the event is not linked to its project")
	}
}

func TestAnEventForAnUnknownProjectIsStillRecorded(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaa", nil, nil)

	runFor(t, collector(h), func() {
		waitFor(t, "the watcher to subscribe", func() bool { return h.engine.Subscriptions() > 0 })
		// Events can arrive before a project has ever been snapshotted. Losing
		// them would mean the first thing a new stack ever does is invisible.
		h.engine.Emit(containerEvent("create", "brand-new", "app"))
		waitFor(t, "the create event to be stored", func() bool {
			return hasEvent(t, h.db, "container.create")
		})
	})

	for _, row := range storedEvents(t, h.db) {
		if row.Type == "container.create" {
			if row.ProjectID.Valid {
				t.Error("an event for an unknown project claims a project id")
			}
			return
		}
	}
	t.Fatal("the event was dropped rather than recorded without a link")
}

func TestAnEventBurstProducesOneSnapshot(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaa", nil, nil)

	runFor(t, collector(h), func() {
		waitFor(t, "the watcher to subscribe", func() bool { return h.engine.Subscriptions() > 0 })
		// What `compose up` on one stack actually looks like. Coalescing is
		// the reason this is one snapshot rather than five.
		for _, action := range []string{"create", "start", "die", "create", "start"} {
			h.engine.Emit(containerEvent(action, "media", "radarr"))
		}
		waitFor(t, "the burst to be coalesced and snapshotted", func() bool {
			return hasEvent(t, h.db, "container.start") && hasEvent(t, h.db, "container.die")
		})
		// Long enough that a second batch would have landed if the window had
		// not held them together.
		time.Sleep(250 * time.Millisecond)
	})

	// The connect reconcile takes the first snapshot; the burst must not add
	// one per event on top of it. Nothing in the burst changes the container,
	// so an unchanged observation touches the existing row rather than
	// inserting — five inserts here would mean coalescing had stopped working.
	if got := len(snapshots(t, h.db)); got != 1 {
		t.Errorf("a five-event burst left %d snapshots, want 1", got)
	}
}

func TestTheCollectorSnapshotsEverythingWhenTheStreamConnects(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaa", nil, nil)

	// Replay is best-effort and the daemon may have dropped the gap, so
	// connecting re-reads the world rather than trusting `since=`. Without
	// this, anything that happened while Silt was down is lost.
	runFor(t, collector(h), func() {
		waitFor(t, "the connect reconcile to snapshot", func() bool {
			return len(snapshots(t, h.db)) > 0
		})
	})
}

func TestSnapshotAllCoversEveryProject(t *testing.T) {
	h := newHarness(t)
	h.engine.SetContainers(twoProjects())
	for _, p := range []string{"media", "monitoring"} {
		id := p + "-app"
		h.engine.SetInspect(id, inspectFor(id, p))
	}

	if err := h.snap.SnapshotAll(context.Background(), "interval"); err != nil {
		t.Fatalf("SnapshotAll: %v", err)
	}

	projects, err := h.db.RQ.ListProjects(context.Background(), 1)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("SnapshotAll recorded %d projects, want 2", len(projects))
	}
	for _, p := range projects {
		if len(snapshotsFor(t, h.db, p.ID)) == 0 {
			t.Errorf("%s has no snapshot", p.Name)
		}
	}
}

func TestSnapshotProjectTakesOneByID(t *testing.T) {
	// The path behind the "Snapshot now" button, which had no test at all.
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaa", nil, nil)
	h.snapshot(t, "manual")

	projects, err := h.db.RQ.ListProjects(context.Background(), 1)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	before := len(snapshotsFor(t, h.db, projects[0].ID))

	// A real change, or the second snapshot is a touch rather than a row.
	h.serve("media", "radarr", "radarr:5.6.0", "sha256:bbb", nil, nil)
	if err := h.snap.SnapshotProject(context.Background(), projects[0].ID); err != nil {
		t.Fatalf("SnapshotProject: %v", err)
	}

	if after := len(snapshotsFor(t, h.db, projects[0].ID)); after != before+1 {
		t.Errorf("snapshots = %d, want %d", after, before+1)
	}
}

func TestSnapshotProjectRefusesAnIDThatIsNotThere(t *testing.T) {
	h := newHarness(t)
	if err := h.snap.SnapshotProject(context.Background(), 9999); err == nil {
		t.Error("snapshotting a project that does not exist succeeded")
	}
}

// containerEvent is what the engine actually puts on the wire, so the test
// exercises the parsing as well as everything after it.
func containerEvent(action, project, service string) events.Message {
	return events.Message{
		Type:   events.ContainerEventType,
		Action: events.Action(action),
		Actor: events.Actor{
			ID: project + "-" + service,
			Attributes: map[string]string{
				docker.LabelProject: project,
				docker.LabelService: service,
				"image":             "example/" + service + ":latest",
			},
		},
		TimeNano: time.Now().UnixNano(),
	}
}

// twoProjects is one container in each of two projects, for the passes that
// are supposed to cover the whole host rather than one stack.
func twoProjects() []container.Summary {
	return []container.Summary{
		dockertest.Container("media-app", "media", "app", "app:1.0", "/srv/media"),
		dockertest.Container("monitoring-app", "monitoring", "app", "app:1.0", "/srv/monitoring"),
	}
}

func inspectFor(id, project string) container.InspectResponse {
	return dockertest.Inspect(id, project+"-app-1", "app:1.0", "sha256:"+project, nil)
}

func snapshots(t *testing.T, db *store.Store) []sqlcgen.Snapshot {
	t.Helper()
	projects, err := db.RQ.ListProjects(context.Background(), 1)
	if err != nil || len(projects) == 0 {
		return nil
	}
	var all []sqlcgen.Snapshot
	for _, p := range projects {
		all = append(all, snapshotsFor(t, db, p.ID)...)
	}
	return all
}

func snapshotsFor(t *testing.T, db *store.Store, projectID int64) []sqlcgen.Snapshot {
	t.Helper()
	rows, err := db.RQ.ListSnapshots(context.Background(), sqlcgen.ListSnapshotsParams{
		ProjectID: projectID, Before: store.Now() + time.Hour.Milliseconds(), MaxRows: 100,
	})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	return rows
}
