// Package web serves the compiled Svelte single-page application from the Go
// binary.
//
// The embedded directory is committed with a .gitkeep placeholder so that
// `go build ./...` succeeds on a clean checkout, before npm has ever run. The
// "all:" prefix is load-bearing: without it the embed pattern skips dotfiles,
// finds no matching files in an otherwise-empty dist, and fails to compile.
// See PROJECT.md Section 12.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// ErrNotBuilt reports that the binary was compiled without a frontend build.
var ErrNotBuilt = errors.New("web: no frontend build embedded in this binary")

// notBuiltMessage is served in place of the SPA when the binary carries no
// frontend. Development builds hit this constantly; saying so plainly beats a
// bare 404 that looks like a routing bug.
const notBuiltMessage = `Silt is running, but this binary was built without the web UI.

Run "make web" (or "npm --prefix web run build") and rebuild, or use a
release image, which always embeds the UI.

The API is unaffected: try /healthz.
`

// FS returns the embedded frontend rooted at the build output directory.
// It returns ErrNotBuilt if no frontend was compiled into this binary.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, ErrNotBuilt
	}
	return sub, nil
}

// Handler serves the SPA, falling back to index.html for any path that does
// not match an embedded file so client-side routing works on deep links.
func Handler() http.Handler {
	dist, err := FS()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuiltMessage))
		})
	}

	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}

		info, err := fs.Stat(dist, name)
		if err != nil || info.IsDir() {
			// Unknown path: hand it to the SPA router rather than 404ing.
			serveIndex(w, r, dist)
			return
		}

		// Vite fingerprints asset filenames, so they are safe to cache hard.
		// index.html must not be, or clients pin themselves to a stale build.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(index)
}
