// Command silt observes Docker Compose stacks and records what changes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/collect"
	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/notify"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/settings"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/web"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet if configuration failed.
		fmt.Fprintf(os.Stderr, "silt: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// A LevelVar rather than a fixed level: the log level is editable on the
	// settings screen, and every logger built from this handler follows it.
	logLevel := new(slog.LevelVar)
	logLevel.Set(cfg.Level())
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(log)

	if _, err := web.FS(); errors.Is(err, web.ErrNotBuilt) {
		log.Warn("no web UI embedded in this binary; API only")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dc, err := docker.New(cfg.DockerHost)
	if err != nil {
		return err
	}
	defer func() { _ = dc.Close() }()

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	log.Info("database ready", "path", cfg.DBPath)

	// Generated on first boot and never logged or exposed. Without it the
	// stored value digests would be reversible for any low-entropy secret.
	redactionKey, err := db.RedactionKey(ctx)
	if err != nil {
		return err
	}
	// The environment is the baseline; the database holds overrides on top of
	// it. A stored document that no longer validates is reported and skipped
	// rather than fatal — refusing to start would lock someone out of the very
	// screen that fixes it.
	live, err := settings.Load(ctx, cfg, db)
	if err != nil {
		log.Warn("stored settings ignored", "error", err)
	}
	cfg = live.Get()
	logLevel.Set(cfg.Level())

	redactor := redact.New(redactionKey, cfg.KeepKeys)

	notifyFilter, err := notify.ParseFilter(cfg.NotifyOn, cfg.NotifyMinSeverity)
	if err != nil {
		return err
	}
	sender, err := notify.New(cfg.NotifyURLs, notifyFilter, log)
	if err != nil {
		return err
	}
	// Always a Live, even with nothing configured: notification targets are
	// editable at runtime, and a nil held by the collector could never become
	// a sender.
	notifier := notify.NewLive(sender)
	if sender.Enabled() {
		log.Info("notifications enabled", "targets", len(cfg.NotifyURLs),
			"kinds", cfg.NotifyOn, "min_severity", cfg.NotifyMinSeverity)
	}

	auth, err := api.NewAuth(cfg.TrustProxyAuth, cfg.AuthHeader, cfg.PasswordHash)
	if err != nil {
		return err
	}
	if !auth.Enabled() {
		// Worth saying out loud rather than leaving someone to assume Silt
		// asks for a password by default.
		log.Warn("no authentication configured; anyone who can reach this port has full read access",
			"hint", "set SILT_TRUST_PROXY_AUTH with your reverse proxy, or SILT_PASSWORD_HASH")
	}

	hub := api.NewHub(log)
	fileReader := &compose.FileReader{
		Roots:    cfg.ComposeRoots,
		MaxBytes: cfg.MaxComposeFileBytes,
		Redactor: redactor,
	}
	if fileReader.Enabled() {
		log.Info("compose file capture enabled", "roots", cfg.ComposeRoots)
	}

	snapshotter := &collect.Snapshotter{
		Client:    dc,
		Store:     db,
		Redactor:  redactor,
		Log:       log,
		HostName:  cfg.HostName,
		Endpoint:  cfg.DockerHost,
		Publisher: api.HubPublisher{Hub: hub},
		Notifier:  notifier,
		BaseURLFn: func() string { return live.Get().BaseURL },
		Files:     fileReader,
	}

	retainer := &store.Retainer{
		Store: db,
		Live: func() store.RetentionSettings {
			c := live.Get()
			return store.RetentionSettings{
				Policy: store.RetentionPolicy{
					Changed:   config.Days(c.RetentionDays),
					Unchanged: config.Days(c.UnchangedRetentionDays),
					Events:    config.Days(c.EventRetentionDays),
				},
				Interval: c.RetentionInterval,
				Vacuum:   c.VacuumInterval,
			}
		},
		Log: log,
	}

	// The collector retries forever, so an engine that is down at startup is
	// not fatal: Silt keeps serving and reconnects when the engine returns.
	collector := &collect.Collector{
		Client:      dc,
		Log:         log,
		Snapshotter: snapshotter,
		IntervalFn:  func() time.Duration { return live.Get().SnapshotInterval },
	}

	// Everything that caches a configuration value rather than re-reading it
	// gets pushed the new one. Observe fires once immediately, so this is also
	// where the startup values land.
	live.Observe(func(c config.Config) {
		logLevel.Set(c.Level())
		redactor.SetKeepKeys(c.KeepKeys)
		filter, err := notify.ParseFilter(c.NotifyOn, c.NotifyMinSeverity)
		if err != nil {
			log.Error("notification filter rejected; keeping the previous one", "error", err)
			return
		}
		if err := notifier.Replace(c.NotifyURLs, filter, log); err != nil {
			log.Error("notification targets rejected; keeping the previous ones", "error", err)
		}
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("collector starting", "docker_host", cfg.DockerHost)
		if err := collector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("collector stopped", "error", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := retainer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("retention stopped", "error", err)
		}
	}()

	apiServer := api.New(log, db, hub, cfg, snapshotter)
	apiServer.SetVersion(version)
	apiServer.SetSettings(live)
	apiServer.SetAuth(auth)
	apiServer.SetFiles(fileReader)
	srv := apiServer.HTTPServer(cfg)

	errc := make(chan error, 1)
	go func() {
		log.Info("silt listening", "addr", cfg.ListenAddr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	// One exit path, so a failing listener unwinds the collector exactly like a
	// signal does. Returning early here would run the deferred client Close
	// while the collector goroutine was still using it.
	var runErr error
	select {
	case err := <-errc:
		if err != nil {
			runErr = fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Cancel the collector and stop trapping signals, so a second Ctrl-C
	// terminates rather than being swallowed by a slow shutdown.
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown: %w", err)
	}
	wg.Wait()
	return runErr
}
