package collect

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/store"
)

// DefaultWindow is the per-project coalescing window. One `docker compose up`
// fires a burst of events over roughly this long.
const DefaultWindow = 2 * time.Second

// Collector observes a Docker host and turns activity into snapshots.
type Collector struct {
	Client *docker.Client
	Log    *slog.Logger
	Window time.Duration
	// Snapshotter persists observations. When nil, the collector only logs,
	// which is what the M1 behaviour was.
	Snapshotter *Snapshotter
	// Interval is the reconcile cadence that catches whatever the event
	// stream missed. Zero disables it.
	Interval time.Duration
	// IntervalFn, when set, supersedes Interval and is re-read on every tick.
	// The reconcile cadence is editable from the settings screen, and a ticker
	// built once at startup would keep the old one until a restart.
	IntervalFn func() time.Duration
}

// Run blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	window := c.Window
	if window <= 0 {
		window = DefaultWindow
	}

	coalescer := NewCoalescer(window)
	defer coalescer.Close()

	watcher := &docker.Watcher{
		Client: c.Client,
		Log:    log,
		OnEvent: func(e docker.Event) {
			c.recordDockerEvent(ctx, e)
			coalescer.Add(e)
		},
		OnConnect: func(resumedFrom time.Time) {
			if resumedFrom.IsZero() {
				log.Info("docker event stream connected")
			} else {
				log.Info("docker event stream reconnected", "resumed_from", resumedFrom.UTC())
			}
			// Replay is best-effort and the daemon may have dropped the gap,
			// so re-read the world rather than trusting since=.
			c.reconcile(ctx, log)
			if c.Snapshotter != nil {
				if err := c.Snapshotter.SnapshotAll(ctx, TriggerInterval); err != nil && ctx.Err() == nil {
					log.Error("reconcile snapshot failed", "error", err)
				}
			}
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for batch := range coalescer.C() {
			log.Info("project activity",
				"project", batch.Project,
				"events", len(batch.Events),
				"services", batch.Services(),
				"actions", batch.Actions(),
				"window", batch.Last.Sub(batch.First).Round(time.Millisecond),
			)
			c.snapshotProject(ctx, batch.Project, TriggerEvent)
		}
	}()

	intervalDone := make(chan struct{})
	go func() {
		defer close(intervalDone)
		current := c.interval()
		if current <= 0 || c.Snapshotter == nil {
			<-ctx.Done()
			return
		}
		ticker := time.NewTicker(current)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := c.Snapshotter.SnapshotAll(ctx, TriggerInterval); err != nil && ctx.Err() == nil {
				log.Error("interval reconcile failed", "error", err)
			}
			// Picked up after the pass rather than before, so an edit that
			// shortens the interval does not also fire an extra reconcile.
			if next := c.interval(); next > 0 && next != current {
				log.Info("snapshot interval changed", "from", current, "to", next)
				current = next
				ticker.Reset(current)
			}
		}
	}()

	err := watcher.Run(ctx)
	coalescer.Close()
	<-done
	<-intervalDone
	return err
}

// dockerEventSeverity classifies engine events for the timeline. A container
// dying or turning unhealthy is what someone is looking for at 03:10; a start
// is context.
func dockerEventSeverity(action string) string {
	switch {
	case action == "die", action == "oom", action == "kill":
		return store.SeverityError
	case strings.HasPrefix(action, "health_status: unhealthy"):
		return store.SeverityError
	case action == "stop", action == "restart", action == "pause":
		return store.SeverityWarn
	default:
		return store.SeverityInfo
	}
}

// recordDockerEvent persists an engine event and broadcasts it.
func (c *Collector) recordDockerEvent(ctx context.Context, e docker.Event) {
	if c.Snapshotter == nil || ctx.Err() != nil {
		return
	}

	rec := store.EventRecord{
		Service:  e.Service,
		TS:       e.At.UnixMilli(),
		Source:   store.SourceDocker,
		Type:     e.Type + "." + strings.SplitN(e.Action, ":", 2)[0],
		Severity: dockerEventSeverity(e.Action),
		Actor:    e.ActorID,
		Message:  e.Action,
		Payload:  map[string]any{"project": e.Project, "image": e.Image, "action": e.Action},
	}
	if id, ok := c.projectID(ctx, e.Project); ok {
		rec.ProjectID = &id
	}

	row, err := c.Snapshotter.Store.RecordEvent(ctx, rec)
	if err != nil {
		if ctx.Err() == nil {
			c.Snapshotter.Log.Error("record docker event", "action", e.Action, "error", err)
		}
		return
	}
	if c.Snapshotter.Publisher != nil {
		c.Snapshotter.Publisher.PublishEvent(map[string]any{
			"id":       row.ID,
			"ts":       row.Ts,
			"source":   row.Source,
			"type":     row.Type,
			"severity": row.Severity,
			"service":  row.Service,
			"message":  row.Message,
			"project":  e.Project,
		})
	}
}

// projectID resolves a compose project name to its database id. Events can
// arrive before the project has ever been snapshotted, in which case they are
// still recorded, just without the link.
//
// One indexed lookup rather than listing every host and every project. This
// runs on every Docker event, and a `compose up` across a forty-project host
// produces a burst of them — on the hardware Silt is built for, that was a
// table scan per event to answer a question the UNIQUE (host_id, name) index
// already answers.
func (c *Collector) projectID(ctx context.Context, name string) (int64, bool) {
	if name == "" {
		return 0, false
	}
	id, err := c.Snapshotter.Store.RQ.GetProjectIDByName(ctx, name)
	if err != nil {
		return 0, false
	}
	return id, true
}

// snapshotProject snapshots one project by name, looking it up from the
// engine so the snapshot reflects current state rather than the event payload.
func (c *Collector) snapshotProject(ctx context.Context, name, trigger string) {
	if c.Snapshotter == nil || ctx.Err() != nil {
		return
	}
	projects, err := c.Client.Discover(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.Snapshotter.Log.Error("discovery failed before snapshot", "project", name, "error", err)
		}
		return
	}
	for _, p := range projects {
		if p.Name != name {
			continue
		}
		result, err := c.Snapshotter.Snapshot(ctx, p, trigger)
		if err != nil {
			c.Snapshotter.Log.Error("snapshot failed", "project", name, "error", err)
			return
		}
		c.Snapshotter.logResult(name, trigger, result)
		return
	}
	// The project has no containers left — a `compose down`. Nothing to
	// inspect; the last snapshot already records what was there.
	c.Snapshotter.Log.Info("project has no containers", "project", name)
}

// reconcile re-reads every project from the engine.
func (c *Collector) reconcile(ctx context.Context, log *slog.Logger) {
	projects, err := c.Client.Discover(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("discovery failed", "error", err)
		}
		return
	}
	services := 0
	for _, p := range projects {
		services += len(p.Services)
		log.Info("project discovered",
			"project", p.Name,
			"services", len(p.Services),
			"working_dir", p.WorkingDir,
			"config_files", len(p.ConfigFiles),
		)
	}
	log.Info("reconciled", "projects", len(projects), "services", services)
}

// interval is the reconcile cadence in force right now.
func (c *Collector) interval() time.Duration {
	if c.IntervalFn != nil {
		return c.IntervalFn()
	}
	return c.Interval
}
