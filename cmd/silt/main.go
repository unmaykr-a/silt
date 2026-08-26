// Command silt observes Docker Compose stacks and records what changes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/auth"
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

	gate, err := buildGate(ctx, cfg, db, log)
	if err != nil {
		return err
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
		// Expired sessions are swept alongside everything else rather than on
		// their own timer: it is the same "remove what is past its window"
		// pass, and one schedule is easier to reason about than two.
		Extra: func(ctx context.Context) {
			if removed, err := gate.Sessions.Sweep(ctx); err != nil {
				log.Error("sweep sessions failed", "error", err)
			} else if removed > 0 {
				log.Info("expired sessions removed", "count", removed)
			}
		},
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
	apiServer.SetAuth(gate)
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

// buildGate assembles authentication and says out loud what it did.
//
// Every one of these warnings is about a default that is convenient and not
// safe. None of them stop Silt starting: someone bringing a stack up at 02:00
// needs the tool that tells them what changed, not a refusal to boot.
func buildGate(ctx context.Context, cfg config.Config, db *store.Store, log *slog.Logger) (*api.Gate, error) {
	account, err := auth.LoadAccount(ctx, db, cfg.PasswordHash, cfg.LocalAccount)
	if err != nil {
		return nil, err
	}
	proxy, err := auth.NewProxy(cfg.TrustProxyAuth, cfg.AuthHeader, cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}

	// Discovery reaches the network, so a provider that is down is a warning
	// and a disabled login rather than a refusal to start. Silt's job is to
	// tell you what changed; being unable to do that because an unrelated
	// service is down would be the wrong trade.
	provider, err := auth.NewOIDC(ctx, auth.OIDCConfig{
		Issuer:        cfg.OIDCIssuer,
		ClientID:      cfg.OIDCClientID,
		ClientSecret:  cfg.OIDCClientSecret,
		RedirectURL:   cfg.OIDCCallbackURL(),
		Scopes:        cfg.OIDCScopes,
		UsernameClaim: cfg.OIDCUsernameClaim,
		GroupsClaim:   cfg.OIDCGroupsClaim,
		AllowedGroups: cfg.OIDCAllowedGroups,
		AllowedUsers:  cfg.OIDCAllowedUsers,
	})
	if err != nil {
		log.Error("OpenID Connect is configured but unusable; that login is disabled", "error", err)
		provider = nil
	}

	gate := &api.Gate{
		Sessions:       auth.NewSessions(db, cfg.SessionTTL, cfg.SessionIdleTTL),
		Account:        account,
		Proxy:          proxy,
		OIDC:           provider,
		MetricsPublic:  cfg.MetricsPublic,
		AllowedOrigins: originsOf(cfg.BaseURL),
	}

	switch {
	case !gate.Enabled():
		log.Warn("no authentication configured; anyone who can reach this port has full read access",
			"hint", "set SILT_LOCAL_ACCOUNT=true, SILT_OIDC_ISSUER, or SILT_TRUST_PROXY_AUTH with your reverse proxy")
	case account.SetupRequired():
		// Loud, because there is a real window here: until someone claims the
		// account, whoever reaches the UI first gets to. Everything else is
		// refused meanwhile, and SILT_PASSWORD_HASH removes the window
		// entirely for anyone who would rather not have it.
		log.Warn("waiting for setup: open Silt and choose a password",
			"note", "until then every request is refused, and the first person to reach the UI claims the account")
	default:
		log.Info("authentication enabled",
			"oidc", provider.Enabled(), "proxy", proxy.Enabled(), "account", account.Enabled())
	}
	if proxy.TrustsAnySource() {
		log.Warn("forward auth trusts the identity header from any source; anything that can reach this port can claim to be anyone",
			"hint", "set SILT_TRUSTED_PROXIES to your proxy's address or subnet", "header", proxy.Header())
	}
	if cfg.MetricsPublic {
		log.Warn("/metrics is reachable without authentication and names every project on this host")
	}
	return gate, nil
}

// originsOf returns the origin of a configured base URL, so a request the
// browser addresses to that name is not treated as cross-site.
func originsOf(baseURL string) []string {
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return []string{u.Scheme + "://" + u.Host}
}
