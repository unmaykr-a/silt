package config_test

import (
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
)

// The callback URL and the issuer check had no tests, and both are the kind of
// thing that is only ever wrong once: the callback has to match what is
// registered with the provider character for character, and an issuer reached
// over plain HTTP carries the authorization code and the id_token in clear.

func TestTheCallbackURLIsDerivedFromTheBaseURL(t *testing.T) {
	for _, c := range []struct {
		name, base, explicit, want string
	}{
		{"from the base URL", "https://silt.example.lan", "", "https://silt.example.lan/api/auth/callback"},
		{"trailing slash does not double up", "https://silt.example.lan/", "", "https://silt.example.lan/api/auth/callback"},
		{"a sub-path is kept", "https://example.lan/silt", "", "https://example.lan/silt/api/auth/callback"},
		{"explicit wins", "https://silt.example.lan", "https://other.example.lan/cb", "https://other.example.lan/cb"},
		// Empty rather than a relative path: Silt works the callback out from
		// the request instead, so behind a proxy it is the public name that
		// gets registered.
		{"nothing to derive from", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Config{BaseURL: c.base, OIDCRedirectURL: c.explicit}
			if got := cfg.OIDCCallbackURL(); got != c.want {
				t.Errorf("OIDCCallbackURL() = %q, want %q", got, c.want)
			}
		})
	}
}

// validOIDC is a configuration that passes everything except what each case
// is testing.
func validOIDC(issuer string) config.Config {
	return config.Config{
		ListenAddr:          ":8375",
		LogLevel:            "info",
		DockerHost:          "tcp://docker-socket-proxy:2375",
		DBPath:              "/data/silt.db",
		SnapshotInterval:    5 * time.Minute,
		RetentionInterval:   time.Hour,
		MaxComposeFileBytes: 1 << 20,
		SessionTTL:          720 * time.Hour,
		NotifyMinSeverity:   "medium",
		HostName:            "local",
		OIDCIssuer:          issuer,
		OIDCClientID:        "silt",
	}
}

func TestAnIssuerOverPlainHTTPIsRefusedUnlessItIsLoopback(t *testing.T) {
	for _, c := range []struct {
		issuer string
		ok     bool
	}{
		{"https://auth.example.lan/application/o/silt/", true},
		// A developer running a provider locally has no other option.
		{"http://localhost:9000/application/o/silt/", true},
		{"http://127.0.0.1:9000/", true},
		{"http://[::1]:9000/", true},
		{"http://auth.example.lan/", false},
		// Not loopback, whatever it is named.
		{"http://auth.localhost.example.lan/", false},
		{"http://192.168.1.10:9000/", false},
		{"not a url", false},
		{"/relative", false},
	} {
		cfg := validOIDC(c.issuer)
		err := cfg.Validate()
		if c.ok && err != nil {
			t.Errorf("issuer %q was refused: %v", c.issuer, err)
		}
		if !c.ok && err == nil {
			t.Errorf("issuer %q was accepted", c.issuer)
		}
	}
}

func TestAnIssuerWithoutAClientIDIsRefused(t *testing.T) {
	cfg := validOIDC("https://auth.example.lan/")
	cfg.OIDCClientID = ""
	if err := cfg.Validate(); err == nil {
		t.Error("an issuer with no client id was accepted; the login would fail at the provider")
	}
}
