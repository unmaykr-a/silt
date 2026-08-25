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

// Collector observes a Docker host and reports project-level activity.
//
// M1 reports to the log. Persisting observations as snapshots is M2.
type Collector struct {
	Client *docker.Client
	Log    *slog.Logger
	Window time.Duration
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
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for batch := range coalescer.C() {
			log.Info("project changed",
				"project", batch.Project,
				"events", len(batch.Events),
				"services", batch.Services(),
				"actions", batch.Actions(),
				"window", batch.Last.Sub(batch.First).Round(time.Millisecond),
			)
		}
	}()

	err := watcher.Run(ctx)
	coalescer.Close()
	<-done
	return err
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
