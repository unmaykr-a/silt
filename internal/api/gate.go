package api

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/settings"
	"github.com/unmaykr-a/silt/internal/store"
)

// How long a rebuild triggered by a save may spend on provider discovery.
//
// Discovery reaches the network, and this runs inside the request that saved
// the setting. At startup there is no deadline — a slow provider delays a boot
// that is already happening — but a settings screen that hangs on an
// unreachable issuer is a settings screen nobody can use to fix the issuer.
const gateRebuildTimeout = 5 * time.Second

// WatchAuth installs the gate and keeps it current as the settings behind it
// change.
//
// Authentication is editable from the settings screen since 1.2.0, and every
// part of the gate is built from it. The first build is synchronous and its
// error is returned, so a startup that cannot read the account or parse the
// trusted-proxy list still fails loudly; a later failure keeps the gate that is
// working and says what went wrong, because the alternative is a save that
// locks everyone out.
//
// Only a change to a gate-tagged setting rebuilds. A save hands the whole
// configuration to every observer, so without that check changing the log level
// would re-run OpenID Connect discovery.
//
// One consequence of replacing the gate is worth stating rather than
// discovering: the per-client login throttle lives in the account object, so a
// rebuild resets it. That is not a way past it — only an administrator can save
// a setting, and someone being throttled is by definition not signed in as one
// — but an administrator saving an unrelated authentication change does clear
// whatever backoff was accumulating.
func (s *Server) WatchAuth(ctx context.Context, live *settings.Live) error {
	gate, err := BuildGate(ctx, live.Get(), s.store, s.log)
	if err != nil {
		return err
	}
	s.SetAuth(gate)

	var (
		mu        sync.Mutex
		installed = config.GateFingerprint(live.Get())
	)
	live.Observe(func(c config.Config) {
		fingerprint := config.GateFingerprint(c)
		mu.Lock()
		unchanged := fingerprint == installed
		installed = fingerprint
		mu.Unlock()
		if unchanged {
			return
		}

		// Without the request's cancellation: this runs from the handler that
		// saved the setting, and a gate half-built because the browser
		// navigated away would leave authentication as whatever it was while
		// the stored configuration said otherwise.
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gateRebuildTimeout)
		defer cancel()

		next, err := BuildGate(buildCtx, c, s.store, s.log)
		if err != nil {
			s.log.Error("authentication could not be rebuilt; keeping the previous configuration", "error", err)
			return
		}
		s.SetAuth(next)
		s.log.Info("authentication rebuilt")
	})
	return nil
}

// SweepSessions removes the sessions that have expired, through whichever gate
// is in force.
//
// Exposed rather than letting the caller hold gate.Sessions, because the
// session lifetimes are editable: a caller that captured the startup gate would
// keep sweeping to the TTLs that were configured then.
func (s *Server) SweepSessions(ctx context.Context) (int64, error) {
	g := s.auth()
	if g == nil || g.Sessions == nil {
		return 0, nil
	}
	return g.Sessions.Sweep(ctx)
}

// BuildGate assembles authentication and says out loud what it did.
//
// Every one of these warnings is about a default that is convenient and not
// safe. None of them stop Silt starting: someone bringing a stack up at 02:00
// needs the tool that tells them what changed, not a refusal to boot.
func BuildGate(ctx context.Context, cfg config.Config, db *store.Store, log *slog.Logger) (*Gate, error) {
	account, err := auth.LoadAccount(ctx, db, cfg.PasswordHash, cfg.LocalAccount)
	if err != nil {
		return nil, err
	}
	proxy, err := auth.NewProxy(cfg.TrustProxyAuth, cfg.AuthHeader, cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	proxy = proxy.WithAdminGroups(cfg.AuthGroupsHeader, cfg.AdminGroups)

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
		AdminGroups:   cfg.OIDCAdminGroups,
	})
	oidcError := ""
	if err != nil {
		// Reported to the UI as well as the log. A provider that quietly does
		// not appear, when you configured one, sends you looking at your
		// authentik config rather than at the reason.
		oidcError = err.Error()
		log.Error("OpenID Connect is configured but unusable; that login is disabled", "error", err)
		provider = nil
	}

	gate := &Gate{
		Sessions:       sessions(db, cfg),
		Account:        account,
		Proxy:          proxy,
		OIDC:           provider,
		OIDCError:      oidcError,
		AllowedOrigins: originsOf(cfg.BaseURL),
	}

	switch {
	case !gate.Enabled():
		log.Warn("no authentication configured; anyone who can reach this port has full read access",
			"hint", "set SILT_LOCAL_ACCOUNT=true, SILT_OIDC_ISSUER, or SILT_TRUST_PROXY_AUTH with your reverse proxy")
	case account.SetupRequired() && !provider.Enabled() && !proxy.Enabled():
		// Loud, because there is a real window here: until someone claims the
		// account, whoever reaches the UI first gets to. Everything else is
		// refused meanwhile, and SILT_PASSWORD_HASH removes the window
		// entirely for anyone who would rather not have it.
		log.Warn("waiting for setup: open Silt and choose a password",
			"note", "until then every request is refused, and the first person to reach the UI claims the account")
	case account.SetupRequired():
		// No window here: something else already guards the door, so claiming
		// the account needs a session rather than being first through it.
		log.Info("sign in with your provider; the built-in account has no password yet",
			"note", "set one afterwards under Settings → Security, or leave it unset")
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
	if provider.Enabled() && !provider.Configured() {
		// Derived per request rather than pinned. It works, and it is worth
		// knowing which URL to register with the provider.
		log.Info("OpenID Connect callback URL is derived from each request",
			"hint", "set SILT_BASE_URL to pin it, and register <base>/api/auth/callback with your provider")
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

// sessions builds the session issuer, including how long a provider-granted
// administrator role is good for.
func sessions(db *store.Store, cfg config.Config) *auth.Sessions {
	s := auth.NewSessions(db, cfg.SessionTTL, cfg.SessionIdleTTL)
	s.AdminTTL = cfg.OIDCAdminTTL
	return s
}
