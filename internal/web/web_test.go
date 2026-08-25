package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// built reports whether this test binary was compiled with a frontend build.
// Both states are legitimate: CI runs a Go-only job specifically to prove the
// binary compiles and behaves without one.
func built(t *testing.T) bool {
	t.Helper()
	_, err := FS()
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrNotBuilt):
		return false
	default:
		t.Fatalf("FS(): %v", err)
		return false
	}
}

func TestHandlerWithoutBuildIsExplicit(t *testing.T) {
	if built(t) {
		t.Skip("frontend is built into this binary")
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "without the web UI") {
		t.Errorf("body does not explain the missing build: %q", rec.Body.String())
	}
}

func TestHandlerServesIndex(t *testing.T) {
	if !built(t) {
		t.Skip("no frontend embedded in this binary")
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", cc)
	}
}

// Deep links belong to the client-side router, not to a 404.
func TestUnknownPathFallsBackToIndex(t *testing.T) {
	if !built(t) {
		t.Skip("no frontend embedded in this binary")
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/42/diff", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<div id=\"app\"") {
		t.Errorf("fallback did not serve index.html: %q", truncate(rec.Body.String()))
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
