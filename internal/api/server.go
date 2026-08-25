// Package api exposes Silt's HTTP surface: the REST API, the SSE stream, and
// the embedded web UI.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/web"
)

// Server wires the HTTP routes together.
type Server struct {
	log *slog.Logger
}

// New returns a Server ready to be mounted.
func New(log *slog.Logger) *Server {
	return &Server{log: log}
}

// Handler builds the route tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	// Everything not claimed by an API route belongs to the SPA.
	mux.Handle("/", web.Handler())
	return s.logRequests(mux)
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
