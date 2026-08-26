// Package api exposes Silt's HTTP surface: the REST API, the SSE stream, the
// ingest webhook, and the embedded web UI.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/settings"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/web"
)

// Snapshotter is the collector capability the API needs, kept narrow so the
// api package does not depend on the whole collector.
type Snapshotter interface {
	SnapshotProject(ctx context.Context, projectID int64) error
}

// Server wires the HTTP routes together.
type Server struct {
	log         *slog.Logger
	store       *store.Store
	hub         *Hub
	cfg         config.Config
	snapshotter Snapshotter
	started     time.Time
	version     string
	auth        *Auth
	files       FileReader
	live        *settings.Live
}

// conf returns the configuration in force right now.
//
// Handlers must call this rather than reading s.cfg: settings are editable at
// runtime, and a handler holding the startup copy would keep reporting — and
// enforcing — a value the operator has already changed.
func (s *Server) conf() config.Config {
	if s.live != nil {
		return s.live.Get()
	}
	return s.cfg
}

// SetVersion records the build version for the settings screen.
func (s *Server) SetVersion(v string) { s.version = v }

// New returns a Server ready to be mounted.
func New(log *slog.Logger, db *store.Store, hub *Hub, cfg config.Config, snap Snapshotter) *Server {
	if log == nil {
		log = slog.Default()
	}
	if hub == nil {
		hub = NewHub(log)
	}
	return &Server{log: log, store: db, hub: hub, cfg: cfg, snapshotter: snap, started: time.Now(), version: "dev"}
}

// SetSettings installs the live settings layer. Without it the server serves
// the startup configuration and reports settings as read-only.
func (s *Server) SetSettings(l *settings.Live) { s.live = l }

// SetFiles installs the compose file reader used by the marking preview.
func (s *Server) SetFiles(f FileReader) { s.files = f }

// SetAuth installs the authentication policy.
func (s *Server) SetAuth(a *Auth) {
	s.auth = a
}

// Hub exposes the event hub so the collector can publish to it.
func (s *Server) Hub() *Hub { return s.hub }

// Handler builds the route tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /metrics", s.metrics)

	mux.HandleFunc("GET /api/hosts", s.listHosts)
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("GET /api/projects/{id}", s.getProject)
	mux.HandleFunc("GET /api/projects/{id}/snapshots", s.listSnapshots)
	mux.HandleFunc("GET /api/projects/{id}/services", s.listProjectServices)
	mux.HandleFunc("GET /api/projects/{id}/services/{service}", s.getServiceHistory)
	mux.HandleFunc("POST /api/projects/{id}/snapshot", s.takeSnapshot)
	mux.HandleFunc("GET /api/snapshots/{id}", s.getSnapshot)
	mux.HandleFunc("GET /api/snapshots/{id}/compose", s.getCompose)
	mux.HandleFunc("GET /api/snapshots/{id}/files", s.listSnapshotFiles)
	mux.HandleFunc("GET /api/snapshots/{id}/file", s.getSnapshotFile)
	mux.HandleFunc("GET /api/diff/file", s.getFileDiff)
	mux.HandleFunc("GET /api/projects/{id}/files", s.listProjectFilePaths)
	mux.HandleFunc("GET /api/projects/{id}/files/preview", s.previewFile)
	mux.HandleFunc("GET /api/projects/{id}/redaction-rules", s.listRedactionRules)
	mux.HandleFunc("POST /api/projects/{id}/redaction-rules", s.postRedactionRule)
	mux.HandleFunc("DELETE /api/projects/{id}/redaction-rules/{rule}", s.deleteRedactionRule)
	mux.HandleFunc("GET /api/diff", s.getDiff)
	mux.HandleFunc("GET /api/events", s.listEvents)
	mux.HandleFunc("GET /api/timeline", s.getTimeline)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("POST /api/ingest", s.ingest)
	mux.HandleFunc("GET /api/auth", s.getAuthState)
	mux.HandleFunc("POST /api/login", s.postLogin)
	mux.HandleFunc("POST /api/logout", s.postLogout)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("DELETE /api/settings", s.deleteSettings)
	mux.HandleFunc("GET /api/version", s.getVersion)
	mux.HandleFunc("POST /api/maintenance/prune", s.postPrune)

	// Anything not claimed by an API route belongs to the SPA.
	mux.Handle("/", web.Handler())
	return s.logRequests(s.requireAuth(mux))
}

// HTTPServer returns a configured *http.Server for cfg.
func (s *Server) HTTPServer(cfg config.Config) *http.Server {
	return &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: s.Handler(),
		// No WriteTimeout: /api/stream is a long-lived SSE connection and any
		// write deadline would sever it mid-stream. Slow-client protection
		// comes from ReadHeaderTimeout and IdleTimeout instead.
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// readyz reports whether Silt can actually serve: healthz says the process is
// up, readyz says the database answers.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	if _, err := s.store.Usage(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

// metrics serves a minimal Prometheus exposition.
//
// Hand-written rather than pulling in the client library: the brief defers
// anything beyond a basic endpoint, and a handful of gauges do not justify a
// dependency and its registry.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	usage, err := s.store.Usage(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	events, err := s.store.RQ.CountEvents(r.Context())
	if err != nil {
		events = 0
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP silt_uptime_seconds Time since the process started.\n")
	fmt.Fprintf(w, "# TYPE silt_uptime_seconds gauge\nsilt_uptime_seconds %.0f\n", time.Since(s.started).Seconds())
	fmt.Fprintf(w, "# HELP silt_blobs Stored content-addressed blobs.\n")
	fmt.Fprintf(w, "# TYPE silt_blobs gauge\nsilt_blobs %d\n", usage.Blobs)
	fmt.Fprintf(w, "# HELP silt_blob_bytes Compressed size of stored blobs.\n")
	fmt.Fprintf(w, "# TYPE silt_blob_bytes gauge\nsilt_blob_bytes %d\n", usage.StoredBytes)
	fmt.Fprintf(w, "# HELP silt_events Recorded events.\n")
	fmt.Fprintf(w, "# TYPE silt_events gauge\nsilt_events %d\n", events)
	fmt.Fprintf(w, "# HELP silt_sse_subscribers Connected SSE clients.\n")
	fmt.Fprintf(w, "# TYPE silt_sse_subscribers gauge\nsilt_sse_subscribers %d\n", s.hub.Subscribers())
}

// --- helpers ---

// APIError is the error body every failing endpoint returns.
type APIError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent; nothing useful left to do.
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIError{Error: msg})
}

// pathID reads a positive integer path parameter.
func pathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, raw)
	}
	return id, nil
}

// queryInt reads an integer query parameter, falling back to def.
func queryInt(r *http.Request, name string, def int64) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// queryLimit clamps a caller-supplied limit so no request can ask for the
// whole database.
func queryLimit(r *http.Request, def, max int64) int64 {
	v := queryInt(r, "limit", def)
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func queryBool(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// constantTimeEqual compares tokens without leaking length or content through
// timing.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// statusRecorder captures the response code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer, which SSE
// needs for flushing.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}

var errNotFound = errors.New("not found")
