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
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/docker"
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

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Level()}))
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

	// The collector retries forever, so an engine that is down at startup is
	// not fatal: Silt keeps serving and reconnects when the engine returns.
	collector := &collect.Collector{Client: dc, Log: log}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("collector starting", "docker_host", cfg.DockerHost)
		if err := collector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("collector stopped", "error", err)
		}
	}()

	srv := api.New(log).HTTPServer(cfg)

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
