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
	if c.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want \":8080\"", c.ListenAddr)
	}
	if c.Level() != slog.LevelInfo {
		t.Errorf("Level() = %v, want info", c.Level())
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
			wantAddr:  ":8080",
		},
		{
			name:      "warning is accepted as an alias for warn",
			env:       map[string]string{"SILT_LOG_LEVEL": "warning"},
			wantLevel: slog.LevelWarn,
			wantAddr:  ":8080",
		},
		{
			name:    "unknown log level is rejected",
			env:     map[string]string{"SILT_LOG_LEVEL": "verbose"},
			wantErr: true,
		},
		{
			name:    "listen address without a port is rejected",
			env:     map[string]string{"SILT_LISTEN_ADDR": "8080"},
			wantErr: true,
		},
		{
			// env treats an empty variable as unset. Falling back to the
			// default beats refusing to start because a compose file has a
			// blank value.
			name:      "empty listen address falls back to the default",
			env:       map[string]string{"SILT_LISTEN_ADDR": ""},
			wantLevel: slog.LevelInfo,
			wantAddr:  ":8080",
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
