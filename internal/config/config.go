// Package config loads Silt's configuration from the environment.
//
// Silt is configured entirely by environment variables, as self-hosters
// expect. Every knob is documented in PROJECT.md Section 13; this struct
// carries the subset that the current milestone actually reads, and grows as
// features land rather than declaring options that do nothing.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Config is the fully validated runtime configuration.
type Config struct {
	// ListenAddr is the address the HTTP server binds to.
	ListenAddr string `env:"SILT_LISTEN_ADDR" envDefault:":8375"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `env:"SILT_LOG_LEVEL" envDefault:"info"`
	// DockerHost is the Docker API endpoint. The documented default is a
	// read-only socket proxy, never the socket itself: mounting
	// /var/run/docker.sock:ro is not a security boundary, because read-only
	// applies to the file and not to the API. See PROJECT.md Section 3.
	DockerHost string `env:"SILT_DOCKER_HOST" envDefault:"tcp://docker-socket-proxy:2375"`
}

// Load reads the environment, applies defaults, and validates the result.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("SILT_LISTEN_ADDR %q is not a valid host:port: %w", c.ListenAddr, err)
	}
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	if _, err := parseLevel(c.LogLevel); err != nil {
		return err
	}
	if err := validateDockerHost(c.DockerHost); err != nil {
		return err
	}
	return nil
}

func validateDockerHost(host string) error {
	u, err := url.Parse(host)
	if err != nil {
		return fmt.Errorf("SILT_DOCKER_HOST %q is not a valid URL: %w", host, err)
	}
	switch u.Scheme {
	case "tcp", "http", "https", "unix":
		return nil
	case "":
		return fmt.Errorf("SILT_DOCKER_HOST %q needs a scheme, e.g. tcp://host:2375", host)
	default:
		return fmt.Errorf("SILT_DOCKER_HOST %q has unsupported scheme %q; want tcp, http, https or unix", host, u.Scheme)
	}
}

// Level returns the parsed slog level. Load guarantees this cannot fail.
func (c Config) Level() slog.Level {
	lvl, err := parseLevel(c.LogLevel)
	if err != nil {
		return slog.LevelInfo
	}
	return lvl
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("SILT_LOG_LEVEL %q is not one of debug, info, warn, error", s)
	}
}
