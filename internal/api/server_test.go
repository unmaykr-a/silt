package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/settings"
	"github.com/unmaykr-a/silt/internal/store"
)

type proj struct{ name string }

func (p proj) ProjectName() string       { return p.name }
func (p proj) ProjectWorkingDir() string { return "/srv/" + p.name }
func (p proj) ConfigFiles() []string     { return []string{"/srv/" + p.name + "/compose.yaml"} }

type fakeSnapshotter struct{ calls []int64 }

func (f *fakeSnapshotter) SnapshotProject(_ context.Context, id int64) error {
	f.calls = append(f.calls, id)
	return nil
}

type fixture struct {
	srv       *httptest.Server
	store     *store.Store
	hub       *api.Hub
	snapshots *fakeSnapshotter
	projectID int64
	snapshotA int64
	snapshotB int64
	ingestTok string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, _ := db.RedactionKey(ctx)
	r := redact.New(key, nil)

	_, projectID, err := db.UpsertHostAndProject(ctx, "local", "tcp://proxy:2375", "28.0", proj{"media"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	build := func(imageID, apiKey, state string) compose.Observation {
		obs, err := compose.Build(
			docker.Project{Name: "media", WorkingDir: "/srv/media"},
			[]compose.ServiceInput{{
				Service: "radarr",
				Inspected: docker.Inspected{
					Config: docker.ContainerConfig{
						Image:   "lscr.io/linuxserver/radarr:latest",
						ImageID: imageID,
						Env:     []string{"PUID=1000", "API_KEY=" + apiKey},
					},
					Runtime: docker.RuntimeState{ContainerID: "c1", State: state, Health: "healthy"},
				},
			}},
			r,
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return obs
	}

	a, err := db.WriteSnapshot(ctx, projectID, store.Now(), "manual", build("sha256:aaaa", "old", "running"))
	if err != nil {
		t.Fatalf("snapshot a: %v", err)
	}
	b, err := db.WriteSnapshot(ctx, projectID, store.Now(), "manual", build("sha256:bbbb", "new", "restarting"))
	if err != nil {
		t.Fatalf("snapshot b: %v", err)
	}

	if _, err := db.RecordEvent(ctx, store.EventRecord{
		ProjectID: &projectID, Service: "radarr", Source: store.SourceDocker,
		Type: "container.die", Severity: store.SeverityError, Message: "die",
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	hub := api.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	snaps := &fakeSnapshotter{}
	cfg := config.Config{
		IngestToken:       "test-token",
		ListenAddr:        ":8375",
		LogLevel:          "info",
		DockerHost:        "tcp://docker-socket-proxy:2375",
		DBPath:            filepath.Join(t.TempDir(), "silt.db"),
		SnapshotInterval:  5 * time.Minute,
		RetentionInterval: time.Hour,
		RetentionDays:     365,

		UnchangedRetentionDays: 7,
		EventRetentionDays:     90,
		NotifyMinSeverity:      "medium",
		MaxComposeFileBytes:    1 << 20,
		SessionTTL:             720 * time.Hour,
		SessionIdleTTL:         168 * time.Hour,
	}
	server := api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, hub, cfg, snaps)
	// The settings layer is part of the surface under test: without it every
	// write returns 503 and the contract test could only ever check the
	// refusal.
	live, err := settings.Load(ctx, cfg, db)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	server.SetSettings(live)
	// A gate with only a session store: no password, no proxy, no provider, so
	// authentication is off and the fixture stays open — but the session
	// endpoints are real rather than reporting themselves unavailable.
	proxy, err := auth.NewProxy(false, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	// The built-in account is switched off here, so this fixture stays open
	// and its tests can exercise the rest of the API without signing in. The
	// account has its own fixture, in account_test.go.
	account, err := auth.LoadAccount(ctx, db, "", false)
	if err != nil {
		t.Fatalf("LoadAccount: %v", err)
	}
	server.SetAuth(&api.Gate{
		Sessions:      auth.NewSessions(db, 720*time.Hour, 0),
		Account:       account,
		Proxy:         proxy,
		MetricsPublic: true,
	})

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	return &fixture{
		srv: ts, store: db, hub: hub, snapshots: snaps,
		projectID: projectID, snapshotA: a.ID, snapshotB: b.ID, ingestTok: "test-token",
	}
}

func (f *fixture) get(t *testing.T, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func (f *fixture) post(t *testing.T, path, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	return f.do(t, http.MethodPost, path, body, headers)
}

func (f *fixture) do(t *testing.T, method, path, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func TestHealthAndReady(t *testing.T) {
	f := newFixture(t)

	if resp, body := f.get(t, "/healthz"); resp.StatusCode != 200 || string(body) != "ok\n" {
		t.Errorf("healthz = %d %q", resp.StatusCode, body)
	}
	if resp, body := f.get(t, "/readyz"); resp.StatusCode != 200 || !strings.Contains(string(body), "ready") {
		t.Errorf("readyz = %d %q", resp.StatusCode, body)
	}
	resp, body := f.get(t, "/metrics")
	if resp.StatusCode != 200 {
		t.Fatalf("metrics = %d", resp.StatusCode)
	}
	for _, want := range []string{"silt_uptime_seconds", "silt_blobs", "silt_events", "silt_sse_subscribers"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

func TestListEndpoints(t *testing.T) {
	f := newFixture(t)

	var hosts []map[string]any
	_, body := f.get(t, "/api/hosts")
	if err := json.Unmarshal(body, &hosts); err != nil || len(hosts) != 1 {
		t.Fatalf("hosts = %s (err %v)", body, err)
	}

	var projects []map[string]any
	_, body = f.get(t, "/api/projects")
	if err := json.Unmarshal(body, &projects); err != nil || len(projects) != 1 {
		t.Fatalf("projects = %s (err %v)", body, err)
	}
	if projects[0]["name"] != "media" {
		t.Errorf("project name = %v", projects[0]["name"])
	}

	var snaps []map[string]any
	_, body = f.get(t, "/api/projects/1/snapshots")
	if err := json.Unmarshal(body, &snaps); err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots = %s (err %v)", body, err)
	}

	var events []map[string]any
	_, body = f.get(t, "/api/events")
	if err := json.Unmarshal(body, &events); err != nil || len(events) == 0 {
		t.Fatalf("events = %s (err %v)", body, err)
	}
}

// Empty collections must serialise as [] rather than null, or every consumer
// has to special-case it.
func TestEmptyCollectionsAreArrays(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/api/events?type=nothing.matches", "/api/projects/999/snapshots"} {
		_, body := f.get(t, path)
		if strings.TrimSpace(string(body)) != "[]" {
			t.Errorf("%s = %s, want []", path, body)
		}
	}
}

func TestGetSnapshotAndCompose(t *testing.T) {
	f := newFixture(t)

	var detail map[string]any
	_, body := f.get(t, "/api/snapshots/1")
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("snapshot = %s (err %v)", body, err)
	}
	services, _ := detail["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("services = %v", detail["services"])
	}

	_, body = f.get(t, "/api/snapshots/1/compose")
	if !strings.Contains(string(body), "radarr") {
		t.Errorf("compose json missing service: %s", body)
	}
	if strings.Contains(string(body), `"old"`) {
		t.Errorf("compose json leaked a secret value: %s", body)
	}

	resp, body := f.get(t, "/api/snapshots/1/compose?format=yaml")
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Errorf("yaml content-type = %q", ct)
	}
	if !strings.Contains(string(body), "radarr") {
		t.Errorf("compose yaml missing service: %s", body)
	}
}

func TestDiffEndpoint(t *testing.T) {
	f := newFixture(t)

	resp, body := f.get(t, "/api/diff?from=1&to=2")
	if resp.StatusCode != 200 {
		t.Fatalf("diff = %d %s", resp.StatusCode, body)
	}
	var res struct {
		From    map[string]any   `json:"from"`
		To      map[string]any   `json:"to"`
		Summary map[string]int   `json:"summary"`
		Changes []map[string]any `json:"changes"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if res.Summary["image_id"] != 1 {
		t.Errorf("expected an image_id change: %s", body)
	}
	if strings.Contains(string(body), `"old"`) || strings.Contains(string(body), `"new"`) {
		t.Errorf("diff leaked secret values: %s", body)
	}
}

func TestDiffValidation(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/api/diff", "/api/diff?from=1", "/api/diff?from=abc&to=2"} {
		if resp, _ := f.get(t, path); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", path, resp.StatusCode)
		}
	}
	if resp, _ := f.get(t, "/api/diff?from=1&to=9999"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown snapshot should 404, got %d", resp.StatusCode)
	}
}

func TestNotFoundAndBadIDs(t *testing.T) {
	f := newFixture(t)
	cases := map[string]int{
		"/api/projects/9999":  http.StatusNotFound,
		"/api/snapshots/9999": http.StatusNotFound,
		"/api/projects/abc":   http.StatusBadRequest,
		"/api/projects/-1":    http.StatusBadRequest,
	}
	for path, want := range cases {
		if resp, _ := f.get(t, path); resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestManualSnapshot(t *testing.T) {
	f := newFixture(t)

	resp, _ := f.post(t, "/api/projects/1/snapshot", "", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("manual snapshot = %d", resp.StatusCode)
	}
	if len(f.snapshots.calls) != 1 || f.snapshots.calls[0] != 1 {
		t.Errorf("snapshotter calls = %v", f.snapshots.calls)
	}

	if resp, _ := f.post(t, "/api/projects/9999/snapshot", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", resp.StatusCode)
	}
}

func TestTimeline(t *testing.T) {
	f := newFixture(t)

	resp, body := f.get(t, "/api/timeline")
	if resp.StatusCode != 200 {
		t.Fatalf("timeline = %d %s", resp.StatusCode, body)
	}
	var tl struct {
		From     int64            `json:"from"`
		To       int64            `json:"to"`
		BucketMS int64            `json:"bucket_ms"`
		Buckets  []map[string]any `json:"buckets"`
		Changes  []map[string]any `json:"changes"`
		Events   []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if tl.BucketMS <= 0 {
		t.Errorf("bucket_ms = %d", tl.BucketMS)
	}
	if len(tl.Changes) == 0 {
		t.Errorf("timeline reported no changes: %s", body)
	}
	if len(tl.Events) == 0 {
		t.Errorf("timeline reported no events: %s", body)
	}

	// A caller must not be able to make the server build unbounded buckets.
	_, body = f.get(t, "/api/timeline?from=0&to=99999999999999&bucket=1ms")
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tl.Buckets) > 2000 {
		t.Errorf("server built %d buckets despite the clamp", len(tl.Buckets))
	}

	if resp, _ := f.get(t, "/api/timeline?from=2000&to=1000"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("inverted range should 400, got %d", resp.StatusCode)
	}
}

func TestIngestRequiresToken(t *testing.T) {
	f := newFixture(t)
	body := `{"type":"monitor.down","service":"radarr","severity":"error","message":"down"}`

	if resp, _ := f.post(t, "/api/ingest", body, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", resp.StatusCode)
	}
	if resp, _ := f.post(t, "/api/ingest", body, map[string]string{"Authorization": "Bearer wrong"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", resp.StatusCode)
	}
}

func TestIngestAcceptsHeaderAndQueryToken(t *testing.T) {
	f := newFixture(t)
	body := `{"type":"monitor.down","service":"radarr","severity":"error","message":"Radarr is down"}`

	resp, _ := f.post(t, "/api/ingest", body, map[string]string{"Authorization": "Bearer " + f.ingestTok})
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("bearer token = %d, want 202", resp.StatusCode)
	}
	// Not every webhook source can set headers, so the query form must work.
	resp, _ = f.post(t, "/api/ingest?token="+f.ingestTok, body, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("query token = %d, want 202", resp.StatusCode)
	}
}

func TestIngestValidation(t *testing.T) {
	f := newFixture(t)
	auth := map[string]string{"Authorization": "Bearer " + f.ingestTok}

	if resp, _ := f.post(t, "/api/ingest", `{"message":"no type"}`, auth); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing type = %d, want 400", resp.StatusCode)
	}
	if resp, _ := f.post(t, "/api/ingest", `not json`, auth); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", resp.StatusCode)
	}
	oversized := `{"type":"x","message":"` + strings.Repeat("a", 70<<10) + `"}`
	if resp, _ := f.post(t, "/api/ingest", oversized, auth); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d, want 413", resp.StatusCode)
	}
}

// An unset token must fail closed, not open.
func TestIngestFailsClosedWhenUnconfigured(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	server := api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, nil, config.Config{}, nil)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/ingest", "application/json", strings.NewReader(`{"type":"x"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unconfigured ingest = %d, want 503", resp.StatusCode)
	}
}

// The ingest event must reach a connected SSE client.
func TestSSEDeliversIngestedEvent(t *testing.T) {
	f := newFixture(t)

	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/stream", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("read ready frame: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "event: ready") {
		t.Fatalf("first frame = %q, want a ready event", buf[:n])
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		f.post(t, "/api/ingest?token="+f.ingestTok, `{"type":"monitor.down","message":"radarr down"}`, nil)
	}()

	deadline := time.Now().Add(4 * time.Second)
	var got strings.Builder
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
			if strings.Contains(got.String(), "monitor.down") {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("ingested event never arrived over SSE; got %q", got.String())
}

// A subscriber that stops reading must not block the publisher.
func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	hub := api.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			hub.Publish(api.Message{Event: "event", Data: i})
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}
}

// Monitors are usually named after a service rather than its project, so an
// event carrying only a service name must still land on the right project.
func TestIngestMatchesProjectByServiceName(t *testing.T) {
	f := newFixture(t)
	resp, body := f.post(t, "/api/ingest?token="+f.ingestTok,
		`{"type":"monitor.down","service":"radarr","severity":"error"}`, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest = %d %s", resp.StatusCode, body)
	}

	_, body = f.get(t, "/api/events?type=monitor.down")
	var events []map[string]any
	if err := json.Unmarshal(body, &events); err != nil || len(events) != 1 {
		t.Fatalf("events = %s (err %v)", body, err)
	}
	if events[0]["project_id"] == nil {
		t.Errorf("event named after a service did not resolve to its project: %s", body)
	}
}

// The settings screen reports what is configured, never the secrets
// themselves.
func TestSettingsNeverEchoesTheIngestToken(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/settings")
	if resp.StatusCode != 200 {
		t.Fatalf("settings = %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), f.ingestTok) {
		t.Errorf("settings response leaked the ingest token: %s", body)
	}

	var payload struct {
		Effective   map[string]any `json:"effective"`
		Environment map[string]any `json:"environment"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for name, values := range map[string]map[string]any{
		"effective":   payload.Effective,
		"environment": payload.Environment,
	} {
		if values["ingest_configured"] != true {
			t.Errorf("%s.ingest_configured = %v, want true", name, values["ingest_configured"])
		}
		if _, present := values["ingest_token"]; present {
			t.Errorf("%s has an ingest_token field at all", name)
		}
	}
}

func TestServiceHistory(t *testing.T) {
	f := newFixture(t)

	_, body := f.get(t, "/api/projects/1/services")
	var services []string
	if err := json.Unmarshal(body, &services); err != nil || len(services) != 1 || services[0] != "radarr" {
		t.Fatalf("services = %s (err %v)", body, err)
	}

	resp, body := f.get(t, "/api/projects/1/services/radarr")
	if resp.StatusCode != 200 {
		t.Fatalf("history = %d %s", resp.StatusCode, body)
	}
	var history struct {
		Service      string `json:"service"`
		Observations []struct {
			ImageID string `json:"image_id"`
		} `json:"observations"`
		EnvChanges []struct {
			Key       string `json:"key"`
			Redacted  bool   `json:"redacted"`
			Digest    string `json:"digest"`
			Value     string `json:"value"`
			FirstSeen bool   `json:"first_seen"`
		} `json:"env_changes"`
	}
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(history.Observations) != 2 {
		t.Errorf("observations = %d, want 2", len(history.Observations))
	}

	// The fixture writes API_KEY as "old" then "new": one first sighting plus
	// one change. PUID never changes, so it appears once as a first sighting.
	var apiKeyEntries, puidEntries int
	for _, c := range history.EnvChanges {
		switch c.Key {
		case "API_KEY":
			apiKeyEntries++
			if !c.Redacted {
				t.Error("API_KEY should be redacted")
			}
			if c.Value != "" {
				t.Errorf("API_KEY history carried a value: %q", c.Value)
			}
			if c.Digest == "" {
				t.Error("API_KEY history has no digest to compare")
			}
		case "PUID":
			puidEntries++
			if c.Value != "1000" {
				t.Errorf("PUID value = %q, want 1000 readable", c.Value)
			}
		}
	}
	if apiKeyEntries != 2 {
		t.Errorf("API_KEY entries = %d, want 2 (first sighting plus one change)", apiKeyEntries)
	}
	if puidEntries != 1 {
		t.Errorf("PUID entries = %d, want 1 (unchanged keys are not repeated)", puidEntries)
	}
	if strings.Contains(string(body), `"old"`) || strings.Contains(string(body), `"new"`) {
		t.Errorf("service history leaked secret values: %s", body)
	}
}

func TestManualPrune(t *testing.T) {
	f := newFixture(t)
	resp, body := f.post(t, "/api/maintenance/prune", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("prune = %d %s", resp.StatusCode, body)
	}
	var out map[string]int64
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	for _, key := range []string{"unchanged_snapshots", "changed_snapshots", "events", "blobs"} {
		if _, ok := out[key]; !ok {
			t.Errorf("prune result missing %q: %s", key, body)
		}
	}
}
