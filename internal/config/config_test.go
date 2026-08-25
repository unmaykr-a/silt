package config

import (
	"log/slog"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load with no environment set: %v", err)
	}
	if c.ListenAddr != ":8375" {
		t.Errorf("ListenAddr = %q, want \":8375\"", c.ListenAddr)
	}
	if c.Level() != slog.LevelInfo {
		t.Errorf("Level() = %v, want info", c.Level())
	}
	if c.DockerHost != "tcp://docker-socket-proxy:2375" {
		t.Errorf("DockerHost = %q, want the socket proxy default", c.DockerHost)
	}
}

func TestDockerHostValidation(t *testing.T) {
	tests := []struct {
		host    string
		wantErr bool
	}{
		{"tcp://docker-socket-proxy:2375", false},
		{"tcp://127.0.0.1:2375", false},
		{"http://proxy:2375", false},
		{"https://proxy:2376", false},
		{"unix:///var/run/docker.sock", false},
		// A bare host:port is the most likely mistake and must not be accepted
		// silently, since the client would fail far from the cause.
		{"docker-socket-proxy:2375", true},
		{"ssh://host", true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Setenv("SILT_DOCKER_HOST", tt.host)
			_, err := Load()
			if tt.wantErr && err == nil {
				t.Errorf("Load() accepted %q, want error", tt.host)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Load() rejected %q: %v", tt.host, err)
			}
		})
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantErr   bool
		wantLevel slog.Level
		wantAddr  string
	}{
		{
			name:      "explicit values",
			env:       map[string]string{"SILT_LISTEN_ADDR": "127.0.0.1:9000", "SILT_LOG_LEVEL": "debug"},
			wantLevel: slog.LevelDebug,
			wantAddr:  "127.0.0.1:9000",
		},
		{
			name:      "log level is case-insensitive and trimmed",
			env:       map[string]string{"SILT_LOG_LEVEL": "  WARN "},
			wantLevel: slog.LevelWarn,
			wantAddr:  ":8375",
		},
		{
			name:      "warning is accepted as an alias for warn",
			env:       map[string]string{"SILT_LOG_LEVEL": "warning"},
			wantLevel: slog.LevelWarn,
			wantAddr:  ":8375",
		},
		{
			name:    "unknown log level is rejected",
			env:     map[string]string{"SILT_LOG_LEVEL": "verbose"},
			wantErr: true,
		},
		{
			name:    "listen address without a port is rejected",
			env:     map[string]string{"SILT_LISTEN_ADDR": "8375"},
			wantErr: true,
		},
		{
			// env treats an empty variable as unset. Falling back to the
			// default beats refusing to start because a compose file has a
			// blank value.
			name:      "empty listen address falls back to the default",
			env:       map[string]string{"SILT_LISTEN_ADDR": ""},
			wantLevel: slog.LevelInfo,
			wantAddr:  ":8375",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() = %+v, want error", c)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if c.ListenAddr != tt.wantAddr {
				t.Errorf("ListenAddr = %q, want %q", c.ListenAddr, tt.wantAddr)
			}
			if c.Level() != tt.wantLevel {
				t.Errorf("Level() = %v, want %v", c.Level(), tt.wantLevel)
			}
		})
	}
}
