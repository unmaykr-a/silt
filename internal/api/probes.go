package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Live probes: "is this working right now?", asked rather than inferred.
//
// The setup checks read the configuration and say what looks unintended. They
// cannot say whether the Docker endpoint answers, or whether the compose root
// you mounted is actually there — and those are the two failures that look
// identical to a working install from every other screen. A project with no
// files captured and a project whose files Silt cannot read render the same.
//
// Deliberately on demand rather than on the settings payload: each of these
// touches the network or the filesystem, and a settings screen that hits the
// Docker socket every time it renders is a settings screen nobody should open
// during an incident.

// Prober is the Docker half, injected so the API package keeps no Docker
// dependency and a test can supply an engine that is down.
type Prober interface {
	// DockerVersion pings the engine and returns its reported version.
	DockerVersion(ctx context.Context) (string, error)
}

// SetProber wires the live Docker check.
func (s *Server) SetProber(p Prober) { s.prober = p }

type probeResult struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	// Detail is the answer when OK and the reason when not.
	Detail string `json:"detail"`
	TookMS int64  `json:"took_ms"`
}

type probesResponse struct {
	CheckedAt int64         `json:"checked_at"`
	Probes    []probeResult `json:"probes"`
}

// probeTimeout caps the whole set. A hung Docker endpoint is one of the things
// being tested for, so it must not become a hung request.
const probeTimeout = 5 * time.Second

func (s *Server) getProbes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	cfg := s.conf()
	out := probesResponse{CheckedAt: nowMS(), Probes: []probeResult{}}

	out.Probes = append(out.Probes, timed("docker", "Docker endpoint", func() (string, error) {
		if s.prober == nil {
			return "", fmt.Errorf("no Docker client is wired up in this build")
		}
		v, err := s.prober.DockerVersion(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("engine %s at %s", v, cfg.DockerHost), nil
	}))

	out.Probes = append(out.Probes, timed("database", "Database", func() (string, error) {
		if s.store == nil {
			return "", fmt.Errorf("no database")
		}
		usage, err := s.store.Usage(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s at %s", byteSize(usage.StoredBytes), cfg.DBPath), nil
	}))

	// One per root, because "compose capture is not working" is never the
	// useful answer: which of the three paths is missing is.
	if len(cfg.ComposeRoots) == 0 {
		out.Probes = append(out.Probes, probeResult{
			ID: "compose", Label: "Compose roots", OK: true,
			Detail: "none configured — file capture is off",
		})
	}
	for _, root := range cfg.ComposeRoots {
		out.Probes = append(out.Probes, timed("compose:"+root, "Compose root "+root, func() (string, error) {
			info, err := os.Stat(root)
			if err != nil {
				if os.IsNotExist(err) {
					// The overwhelmingly common cause, and the one the error
					// text alone does not suggest.
					return "", fmt.Errorf("not present in the container — is it mounted?")
				}
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("not a directory")
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				return "", fmt.Errorf("not readable: %w", err)
			}
			return fmt.Sprintf("readable, %d entries", len(entries)), nil
		}))
	}

	writeJSON(w, http.StatusOK, out)
}

// timed runs one probe and records how long it took, which is its own signal:
// a Docker endpoint answering in four seconds is working and worth knowing
// about.
func timed(id, label string, run func() (string, error)) probeResult {
	start := time.Now()
	detail, err := run()
	result := probeResult{ID: id, Label: label, OK: err == nil, Detail: detail}
	result.TookMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Detail = err.Error()
	}
	return result
}

func byteSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1024*1024*1024))
	}
}

func nowMS() int64 { return time.Now().UnixMilli() }
