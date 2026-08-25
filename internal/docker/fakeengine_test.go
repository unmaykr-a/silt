package docker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
)

// fakeEngine is a minimal stand-in for the Docker Engine API.
//
// It exists so the reconnect contract can be tested for real — dropping a
// stream, resuming it, and asserting the reconcile fires — without a running
// daemon. Those are the failure modes M1 is actually about, and mocking the
// client interface would test the mock rather than the contract.
type fakeEngine struct {
	srv *httptest.Server

	mu         sync.Mutex
	containers []container.Summary
	reachable  bool
	gate       chan struct{} // closed to sever the in-flight event stream
	sinceParam []string      // every `since` value the client has sent
	eventsSubs int

	events chan events.Message
}

func newFakeEngine() *fakeEngine {
	f := &fakeEngine{
		reachable: true,
		gate:      make(chan struct{}),
		events:    make(chan events.Message, 64),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// host returns a tcp:// URL, exercising the same scheme handling as production.
func (f *fakeEngine) host() string {
	return strings.Replace(f.srv.URL, "http://", "tcp://", 1)
}

func (f *fakeEngine) close() { f.srv.Close() }

func (f *fakeEngine) setContainers(c []container.Summary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers = c
}

func (f *fakeEngine) setReachable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reachable = v
}

// severStream ends the current /events response, as a proxy restart would,
// and arms a fresh gate for the next subscription.
func (f *fakeEngine) severStream() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.gate)
	f.gate = make(chan struct{})
}

func (f *fakeEngine) currentGate() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gate
}

func (f *fakeEngine) subscriptions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eventsSubs
}

func (f *fakeEngine) sinceValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sinceParam...)
}

// emit queues an event for delivery to a subscribed client.
func (f *fakeEngine) emit(m events.Message) { f.events <- m }

func (f *fakeEngine) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	up := f.reachable
	f.mu.Unlock()

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
		f.mu.Lock()
		list := f.containers
		f.mu.Unlock()
		writeJSON(w, list)

	case strings.HasSuffix(path, "/events"):
		f.serveEvents(w, r)

	default:
		http.Error(w, "not implemented: "+path, http.StatusNotFound)
	}
}

func (f *fakeEngine) serveEvents(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if !f.reachable {
		f.mu.Unlock()
		http.Error(w, "engine down", http.StatusInternalServerError)
		return
	}
	f.eventsSubs++
	f.sinceParam = append(f.sinceParam, r.URL.Query().Get("since"))
	gate := f.gate
	f.mu.Unlock()

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
		case m := <-f.events:
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
