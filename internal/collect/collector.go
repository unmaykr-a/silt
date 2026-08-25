package collect

import (
	"context"
	"log/slog"
	"time"

	"github.com/unmaykr-a/silt/internal/docker"
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
		Client:  c.Client,
		Log:     log,
		OnEvent: coalescer.Add,
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

	var ticker *time.Ticker
	tick := make(<-chan time.Time)
	if c.Interval > 0 && c.Snapshotter != nil {
		ticker = time.NewTicker(c.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	intervalDone := make(chan struct{})
	go func() {
		defer close(intervalDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick:
				if err := c.Snapshotter.SnapshotAll(ctx, TriggerInterval); err != nil && ctx.Err() == nil {
					log.Error("interval reconcile failed", "error", err)
				}
			}
		}
	}()

	err := watcher.Run(ctx)
	coalescer.Close()
	<-done
	<-intervalDone
	return err
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
