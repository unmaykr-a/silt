// Package dockertest provides a stand-in for the Docker Engine API.
//
// It was a test-only type inside internal/docker, which meant internal/collect
// — the package that turns engine responses into snapshots, and the one that
// has actually shipped a bug — could not be tested at all without a running
// daemon. It sat at 22% coverage for exactly that reason.
//
// A fake HTTP engine rather than a mocked client interface: mocking the
// interface tests the mock. The failure modes worth catching are a stream that
// drops, an inspect that omits a field, and a container that changed between
// the list and the inspect — none of which a mock exercises.
package dockertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/image"
)

// Engine is a minimal Docker Engine API served over HTTP.
type Engine struct {
	srv *httptest.Server

	mu         sync.Mutex
	containers []container.Summary
	inspects   map[string]container.InspectResponse
	images     map[string]image.InspectResponse
	reachable  bool
	gate       chan struct{} // closed to sever the in-flight event stream
	sinceParam []string      // every `since` value the client has sent
	eventsSubs int
	// inspectFail names containers whose inspect returns 404, for the race
	// where a container is listed and then removed before it is inspected.
	inspectFail map[string]bool

	events chan events.Message
}

// New starts an engine. Close it when the test finishes.
func New() *Engine {
	e := &Engine{
		reachable:   true,
		gate:        make(chan struct{}),
		events:      make(chan events.Message, 64),
		inspects:    map[string]container.InspectResponse{},
		images:      map[string]image.InspectResponse{},
		inspectFail: map[string]bool{},
	}
	e.srv = httptest.NewServer(http.HandlerFunc(e.handle))
	return e
}

// Host returns a tcp:// URL, exercising the same scheme handling as production.
func (e *Engine) Host() string {
	return strings.Replace(e.srv.URL, "http://", "tcp://", 1)
}

// Close shuts the engine down.
func (e *Engine) Close() { e.srv.Close() }

// SetContainers replaces what `docker ps` returns.
func (e *Engine) SetContainers(c []container.Summary) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.containers = c
}

// SetInspect registers the inspect response for one container id.
func (e *Engine) SetInspect(id string, resp container.InspectResponse) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inspects[id] = resp
}

// SetImage registers the inspect response for one image reference.
func (e *Engine) SetImage(ref string, resp image.InspectResponse) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.images[ref] = resp
}

// FailInspect makes one container's inspect return 404, as it does when a
// container is removed between the list and the inspect.
func (e *Engine) FailInspect(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inspectFail[id] = true
}

// SetReachable turns the whole engine up or down.
func (e *Engine) SetReachable(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reachable = v
}

// SeverStream ends the current /events response, as a proxy restart would, and
// arms a fresh gate for the next subscription.
func (e *Engine) SeverStream() {
	e.mu.Lock()
	defer e.mu.Unlock()
	close(e.gate)
	e.gate = make(chan struct{})
}

// CurrentGate returns the channel closed by the next SeverStream.
func (e *Engine) CurrentGate() chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gate
}

// Subscriptions counts how many times a client has opened /events.
func (e *Engine) Subscriptions() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.eventsSubs
}

// SinceValues returns every `since` the client has sent, which is how the
// reconnect contract is checked: a resumed stream must not replay from zero.
func (e *Engine) SinceValues() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sinceParam...)
}

// Emit queues an event for delivery to a subscribed client.
func (e *Engine) Emit(m events.Message) { e.events <- m }

func (e *Engine) handle(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	up := e.reachable
	e.mu.Unlock()

	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/_ping"):
		if !up {
			http.Error(w, "engine down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Api-Version", "1.51")
		w.Header().Set("Ostype", "linux")
		w.WriteHeader(http.StatusOK)

	case strings.HasSuffix(path, "/version"):
		if !up {
			http.Error(w, "engine down", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"Version": "28.5.2", "ApiVersion": "1.51", "Os": "linux", "Arch": "amd64"})

	case strings.Contains(path, "/containers/json"):
		if !up {
			http.Error(w, "engine down", http.StatusInternalServerError)
			return
		}
		e.mu.Lock()
		list := e.containers
		e.mu.Unlock()
		writeJSON(w, list)

	case strings.HasSuffix(path, "/events"):
		e.serveEvents(w, r)

	case strings.Contains(path, "/containers/") && strings.HasSuffix(path, "/json"):
		e.serveInspect(w, path)

	case strings.Contains(path, "/images/") && strings.HasSuffix(path, "/json"):
		e.serveImage(w, path)

	default:
		http.Error(w, "not implemented: "+path, http.StatusNotFound)
	}
}

// serveInspect answers GET /containers/{id}/json.
func (e *Engine) serveInspect(w http.ResponseWriter, path string) {
	id := segmentBefore(path, "/json", "/containers/")
	e.mu.Lock()
	failed := e.inspectFail[id]
	resp, ok := e.inspects[id]
	up := e.reachable
	e.mu.Unlock()

	if !up {
		http.Error(w, "engine down", http.StatusInternalServerError)
		return
	}
	if failed || !ok {
		http.Error(w, "No such container: "+id, http.StatusNotFound)
		return
	}
	writeJSON(w, resp)
}

// serveImage answers GET /images/{ref}/json.
func (e *Engine) serveImage(w http.ResponseWriter, path string) {
	ref := segmentBefore(path, "/json", "/images/")
	e.mu.Lock()
	resp, ok := e.images[ref]
	up := e.reachable
	e.mu.Unlock()

	if !up {
		http.Error(w, "engine down", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "No such image: "+ref, http.StatusNotFound)
		return
	}
	writeJSON(w, resp)
}

// segmentBefore extracts the path segment between prefix and suffix. The
// Docker client version-prefixes every path (/v1.51/containers/…), so the
// prefix is found rather than assumed to start the path.
func segmentBefore(path, suffix, prefix string) string {
	path = strings.TrimSuffix(path, suffix)
	if i := strings.LastIndex(path, prefix); i >= 0 {
		path = path[i+len(prefix):]
	}
	return path
}

func (e *Engine) serveEvents(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	if !e.reachable {
		e.mu.Unlock()
		http.Error(w, "engine down", http.StatusInternalServerError)
		return
	}
	e.eventsSubs++
	e.sinceParam = append(e.sinceParam, r.URL.Query().Get("since"))
	gate := e.gate
	e.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-gate:
			// Connection severed, as a restarting proxy would.
			return
		case m := <-e.events:
			if err := enc.Encode(m); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Container builds a list entry carrying the Compose labels Silt discovers by.
func Container(id, project, service, image, workingDir string) container.Summary {
	return container.Summary{
		ID:    id,
		Names: []string{"/" + project + "-" + service + "-1"},
		Image: image,
		State: "running",
		Labels: map[string]string{
			"com.docker.compose.project":              project,
			"com.docker.compose.service":              service,
			"com.docker.compose.project.working_dir":  workingDir,
			"com.docker.compose.project.config_files": workingDir + "/compose.yaml",
		},
	}
}

// Inspect builds an inspect response for a running, healthy container.
func Inspect(id, name, imageRef, imageID string, env []string) container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    id,
			Name:  "/" + name,
			Image: imageID,
			State: &container.State{
				Status:    "running",
				Health:    &container.Health{Status: "healthy"},
				StartedAt: time.Now().Add(-time.Hour).Format(time.RFC3339Nano),
			},
			HostConfig: &container.HostConfig{},
		},
		Config: &container.Config{Image: imageRef, Env: env},
	}
}

// Image builds an image inspect response.
func Image(id string, repoDigests []string) image.InspectResponse {
	return image.InspectResponse{
		ID:          id,
		RepoDigests: repoDigests,
		Created:     time.Now().Add(-24 * time.Hour).Format(time.RFC3339Nano),
	}
}
