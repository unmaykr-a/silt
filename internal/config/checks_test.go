package config_test

import (
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/config"
)

// A configured install: everything the checks look for, satisfied. Each test
// below breaks exactly one thing, so a finding can only come from that break.
func configured() config.Config {
	return config.Config{
		LocalAccount:           true,
		TrustProxyAuth:         false,
		ComposeRoots:           []string{"/srv"},
		NotifyURLs:             []string{"ntfy://ntfy.sh/topic"},
		BaseURL:                "https://silt.example.com",
		IngestToken:            "t",
		RetentionDays:          365,
		UnchangedRetentionDays: 7,
		EventRetentionDays:     90,
	}
}

func find(t *testing.T, checks []config.Check, id string) config.Check {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	var ids []string
	for _, c := range checks {
		ids = append(ids, c.ID)
	}
	t.Fatalf("no check %q; got %v", id, ids)
	return config.Check{}
}

func absent(t *testing.T, checks []config.Check, id string) {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			t.Errorf("unexpected check %q: %s", id, c.Title)
		}
	}
}

func TestAConfiguredInstallHasNothingToWarnAbout(t *testing.T) {
	for _, c := range configured().Checks() {
		if c.Level != config.LevelInfo {
			t.Errorf("%s: level %s on a configured install — %s", c.ID, c.Level, c.Title)
		}
	}
}

func TestNoAuthenticationIsAnError(t *testing.T) {
	c := configured()
	c.LocalAccount = false
	// Anyone who can reach the port has the whole history. Nothing else in
	// the configuration matters as much as this.
	got := find(t, c.Checks(), "auth.none")
	if got.Level != config.LevelError {
		t.Errorf("level = %s, want error", got.Level)
	}
}

func TestAnyAuthenticationMethodSatisfiesIt(t *testing.T) {
	for name, apply := range map[string]func(*config.Config){
		"local account": func(c *config.Config) { c.LocalAccount = true },
		"oidc":          func(c *config.Config) { c.OIDCIssuer = "https://id.example.com" },
		"password hash": func(c *config.Config) { c.PasswordHash = "$2a$12$x" },
		"forward auth": func(c *config.Config) {
			c.TrustProxyAuth = true
			c.TrustedProxies = []string{"172.18.0.0/16"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := configured()
			c.LocalAccount = false
			apply(&c)
			absent(t, c.Checks(), "auth.none")
		})
	}
}

func TestForwardAuthWithoutATrustListIsAnError(t *testing.T) {
	c := configured()
	c.TrustProxyAuth = true
	// The header is settable by anything that can open a socket, so with no
	// trust list "authenticated" means "reached the port".
	got := find(t, c.Checks(), "auth.untrusted-proxy")
	if got.Level != config.LevelError {
		t.Errorf("level = %s, want error", got.Level)
	}

	c.TrustedProxies = []string{"172.18.0.0/16"}
	absent(t, c.Checks(), "auth.untrusted-proxy")
}

func TestOIDCWithNoAllowlistWarns(t *testing.T) {
	c := configured()
	c.OIDCIssuer = "https://id.example.com"
	if got := find(t, c.Checks(), "auth.oidc-open"); got.Level != config.LevelWarn {
		t.Errorf("level = %s, want warn", got.Level)
	}

	c.OIDCAllowedGroups = []string{"admins"}
	absent(t, c.Checks(), "auth.oidc-open")
}

func TestNotificationsWithoutABaseURLWarn(t *testing.T) {
	c := configured()
	c.BaseURL = ""
	// The link is the useful half of the message that arrives during an
	// outage, and nothing tries to send until then.
	if got := find(t, c.Checks(), "notify.no-base-url"); got.Level != config.LevelWarn {
		t.Errorf("level = %s, want warn", got.Level)
	}
}

func TestNoTargetsIsNotedRatherThanWarnedAbout(t *testing.T) {
	c := configured()
	c.NotifyURLs = nil
	c.BaseURL = ""
	// Not having notifications is a choice; a base URL missing under
	// notifications that exist is a mistake. Only one of these should fire.
	if got := find(t, c.Checks(), "notify.off"); got.Level != config.LevelInfo {
		t.Errorf("level = %s, want info", got.Level)
	}
	absent(t, c.Checks(), "notify.no-base-url")
}

func TestInvertedRetentionWarns(t *testing.T) {
	c := configured()
	c.UnchangedRetentionDays = 400
	got := find(t, c.Checks(), "retention.inverted")
	if got.Level != config.LevelWarn {
		t.Errorf("level = %s, want warn", got.Level)
	}
	// The numbers belong in the message: "backwards" without them makes the
	// reader go and look.
	if !strings.Contains(got.Detail, "400") || !strings.Contains(got.Detail, "365") {
		t.Errorf("detail does not name both windows: %s", got.Detail)
	}
}

func TestKeepingEverythingForeverWarnsOnlyWhenEverythingIsForever(t *testing.T) {
	c := configured()
	c.RetentionDays, c.UnchangedRetentionDays, c.EventRetentionDays = 0, 0, 0
	find(t, c.Checks(), "retention.forever")
	// Forever for changed snapshots alone is a normal choice: they are the
	// deduplicated half.
	c.EventRetentionDays = 90
	absent(t, c.Checks(), "retention.forever")
	// And it must not then claim the windows are inverted, since zero means
	// forever rather than nothing.
	absent(t, c.Checks(), "retention.inverted")
}

func TestKeepKeysAreSpelledOut(t *testing.T) {
	c := configured()
	c.KeepKeys = []string{"PUBLIC_*", "APP_ENV"}
	got := find(t, c.Checks(), "redact.keep-keys")
	for _, want := range []string{"PUBLIC_*", "APP_ENV", "2 extra"} {
		if !strings.Contains(got.Title+got.Detail, want) {
			t.Errorf("check does not mention %q: %s / %s", want, got.Title, got.Detail)
		}
	}
}

func TestEveryCheckNamesTheVariablesItIsAbout(t *testing.T) {
	// A finding you cannot act on is a finding that gets ignored.
	c := config.Config{}
	for _, got := range c.Checks() {
		if len(got.EnvVars) == 0 {
			t.Errorf("%s names no environment variable", got.ID)
		}
		if got.Title == "" || got.Detail == "" {
			t.Errorf("%s has an empty title or detail", got.ID)
		}
	}
}
