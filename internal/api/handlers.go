package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/unmaykr-a/silt/internal/diff"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// --- response shapes ---
//
// These are declared rather than returning database rows directly, so the
// wire format is a deliberate contract and a schema change cannot silently
// alter the API. api/openapi.yaml describes exactly these.

type hostResponse struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	DockerVersion string `json:"docker_version,omitempty"`
	LastSeenAt    int64  `json:"last_seen_at,omitempty"`
}

type projectResponse struct {
	ID          int64    `json:"id"`
	HostID      int64    `json:"host_id"`
	Name        string   `json:"name"`
	WorkingDir  string   `json:"working_dir,omitempty"`
	ConfigFiles []string `json:"config_files"`
	FirstSeenAt int64    `json:"first_seen_at"`
	LastSeenAt  int64    `json:"last_seen_at"`
	Archived    bool     `json:"archived"`
}

type snapshotResponse struct {
	ID               int64  `json:"id"`
	ProjectID        int64  `json:"project_id"`
	TakenAt          int64  `json:"taken_at"`
	LastObservedAt   int64  `json:"last_observed_at"`
	ObservationCount int64  `json:"observation_count"`
	Trigger          string `json:"trigger"`
	ComposeSource    string `json:"compose_source"`
	ConfigChanged    bool   `json:"config_changed"`
	RuntimeChanged   bool   `json:"runtime_changed"`
}

type serviceStateResponse struct {
	Service       string `json:"service"`
	ContainerID   string `json:"container_id,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	ImageRef      string `json:"image_ref,omitempty"`
	ImageID       string `json:"image_id,omitempty"`
	ImageDigest   string `json:"image_digest,omitempty"`
	State         string `json:"state,omitempty"`
	Health        string `json:"health,omitempty"`
	RestartCount  int64  `json:"restart_count"`
	StartedAt     *int64 `json:"started_at,omitempty"`
	// ExitCode is present only when this container had stopped. Absent means
	// "did not exit", not "exited cleanly".
	ExitCode  *int64 `json:"exit_code,omitempty"`
	OOMKilled bool   `json:"oom_killed,omitempty"`
}

type snapshotDetailResponse struct {
	snapshotResponse
	Services []serviceStateResponse `json:"services"`
}

type eventResponse struct {
	ID        int64           `json:"id"`
	ProjectID *int64          `json:"project_id,omitempty"`
	Service   string          `json:"service,omitempty"`
	TS        int64           `json:"ts"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Severity  string          `json:"severity"`
	Actor     string          `json:"actor,omitempty"`
	Message   string          `json:"message,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// --- handlers ---

func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.RQ.ListHosts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read hosts")
		return
	}
	out := make([]hostResponse, 0, len(rows))
	for _, h := range rows {
		out = append(out, hostResponse{
			ID:            h.ID,
			Name:          h.Name,
			Endpoint:      h.Endpoint,
			DockerVersion: h.DockerVersion.String,
			LastSeenAt:    h.LastSeenAt.Int64,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	hostID := queryInt(r, "host", 0)
	if hostID == 0 {
		// Default to the only host rather than making a single-host install
		// pass an id it cannot know.
		hosts, err := s.store.RQ.ListHosts(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read hosts")
			return
		}
		if len(hosts) == 0 {
			writeJSON(w, http.StatusOK, []projectResponse{})
			return
		}
		hostID = hosts[0].ID
	}

	rows, err := s.store.RQ.ListProjects(r.Context(), hostID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read projects")
		return
	}
	out := make([]projectResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, toProjectResponse(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.store.RQ.GetProject(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read project")
		return
	}
	writeJSON(w, http.StatusOK, toProjectResponse(p))
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	before := queryInt(r, "before", 0)
	if before <= 0 {
		before = store.Now() + 1
	}
	changedOnly := int64(0)
	if queryBool(r, "changed_only") {
		changedOnly = 1
	}

	rows, err := s.store.RQ.ListSnapshots(r.Context(), sqlcgen.ListSnapshotsParams{
		ProjectID:   id,
		Before:      before,
		ChangedOnly: changedOnly,
		MaxRows:     queryLimit(r, 50, 500),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read snapshots")
		return
	}
	out := make([]snapshotResponse, 0, len(rows))
	for _, snap := range rows {
		out = append(out, toSnapshotResponse(snap))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) takeSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.snapshotter == nil {
		writeError(w, http.StatusServiceUnavailable, "collector not running")
		return
	}
	if _, err := s.store.RQ.GetProject(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err := s.snapshotter.SnapshotProject(r.Context(), id); err != nil {
		s.log.Error("manual snapshot failed", "project_id", id, "error", err)
		writeError(w, http.StatusBadGateway, "snapshot failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "taken"})
}

func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := s.store.RQ.GetSnapshot(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read snapshot")
		return
	}
	states, err := s.store.RQ.ListServiceStates(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read service states")
		return
	}

	out := snapshotDetailResponse{
		snapshotResponse: toSnapshotResponse(snap),
		Services:         make([]serviceStateResponse, 0, len(states)),
	}
	for _, st := range states {
		out.Services = append(out.Services, serviceStateResponse{
			Service:       st.Service,
			ContainerID:   st.ContainerID,
			ContainerName: st.ContainerName,
			ImageRef:      st.ImageRef,
			ImageID:       st.ImageID,
			ImageDigest:   st.ImageDigest,
			State:         st.State,
			Health:        st.Health,
			RestartCount:  st.RestartCount,
			ExitCode:      nullableInt64(st.ExitCode),
			OOMKilled:     st.OomKilled != 0,
			StartedAt:     st.StartedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getCompose(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	model, err := s.store.LoadSnapshotModel(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read snapshot")
		return
	}

	// This is the redacted model, so it is safe to hand out — but it is a
	// record of what ran, not a file to re-apply.
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "yaml", "yml":
		encoded, err := yaml.Marshal(model.Project)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encode yaml")
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(encoded)
	default:
		writeJSON(w, http.StatusOK, model.Project)
	}
}

func (s *Server) getDiff(w http.ResponseWriter, r *http.Request) {
	fromID := queryInt(r, "from", 0)
	toID := queryInt(r, "to", 0)
	if fromID <= 0 || toID <= 0 {
		writeError(w, http.StatusBadRequest, "from and to must both be snapshot ids")
		return
	}

	from, err := s.store.LoadSnapshotModel(r.Context(), fromID)
	if err != nil {
		writeSnapshotLoadError(w, err, fromID)
		return
	}
	to, err := s.store.LoadSnapshotModel(r.Context(), toID)
	if err != nil {
		writeSnapshotLoadError(w, err, toID)
		return
	}

	writeJSON(w, http.StatusOK, diff.Compute(toDiffInput(from), toDiffInput(to)))
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	from := queryInt(r, "from", 0)
	to := queryInt(r, "to", 0)
	if to <= 0 {
		to = store.Now()
	}

	rows, err := s.store.RQ.ListEvents(r.Context(), sqlcgen.ListEventsParams{
		FromTs:    from,
		ToTs:      to,
		ProjectID: queryInt(r, "project", 0),
		Service:   r.URL.Query().Get("service"),
		Type:      r.URL.Query().Get("type"),
		Severity:  r.URL.Query().Get("severity"),
		MaxRows:   queryLimit(r, 100, 1000),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events")
		return
	}
	writeJSON(w, http.StatusOK, toEventResponses(rows))
}

// timelineResponse merges change markers and events onto one axis, which is
// the whole point of the app.
type timelineResponse struct {
	From    int64              `json:"from"`
	To      int64              `json:"to"`
	Bucket  int64              `json:"bucket_ms"`
	Buckets []timelineBucket   `json:"buckets"`
	Changes []snapshotResponse `json:"changes"`
	Events  []eventResponse    `json:"events"`
}

type timelineBucket struct {
	Start   int64 `json:"start"`
	Changes int   `json:"changes"`
	Events  int   `json:"events"`
	Errors  int   `json:"errors"`
}

func (s *Server) getTimeline(w http.ResponseWriter, r *http.Request) {
	to := queryInt(r, "to", 0)
	if to <= 0 {
		to = store.Now()
	}
	from := queryInt(r, "from", 0)
	if from <= 0 {
		from = to - (24 * time.Hour).Milliseconds()
	}
	if from > to {
		writeError(w, http.StatusBadRequest, "from must be before to")
		return
	}
	projectID := queryInt(r, "project", 0)
	bucket := resolveBucket(r, from, to)

	snaps, err := s.store.RQ.SnapshotsInRange(r.Context(), sqlcgen.SnapshotsInRangeParams{
		FromTs:    from,
		ToTs:      to,
		ProjectID: projectID,
		MaxRows:   2000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read snapshots")
		return
	}
	events, err := s.store.RQ.ListEvents(r.Context(), sqlcgen.ListEventsParams{
		FromTs:    from,
		ToTs:      to,
		ProjectID: projectID,
		MaxRows:   2000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events")
		return
	}

	out := timelineResponse{
		From:    from,
		To:      to,
		Bucket:  bucket,
		Changes: make([]snapshotResponse, 0, len(snaps)),
		Events:  toEventResponses(events),
	}

	// Zero-fill the whole window. A density strip needs a point for every
	// bucket, not only the ones with activity: a sparse series leaves the
	// chart to infer its own x-range, and with one or two points that range
	// is meaningless.
	// The +1 covers the partial bucket at the end of the window, which can
	// push the count one past the cap when the span divides exactly.
	count := int((to-from)/bucket) + 1
	if count > maxTimelineBuckets {
		count = maxTimelineBuckets
	}
	buckets := make([]timelineBucket, count)
	for i := range buckets {
		buckets[i].Start = from + int64(i)*bucket
	}
	at := func(ts int64) *timelineBucket {
		i := int((ts - from) / bucket)
		if i < 0 {
			i = 0
		}
		if i >= len(buckets) {
			i = len(buckets) - 1
		}
		return &buckets[i]
	}

	for _, snap := range snaps {
		if snap.ConfigChanged == 1 {
			out.Changes = append(out.Changes, toSnapshotResponse(snap))
			at(snap.TakenAt).Changes++
		}
	}
	for _, e := range events {
		b := at(e.Ts)
		b.Events++
		if e.Severity == store.SeverityError {
			b.Errors++
		}
	}

	out.Buckets = buckets
	writeJSON(w, http.StatusOK, out)
}

// maxTimelineBuckets bounds how much work one timeline request can ask for.
const maxTimelineBuckets = 2000

// resolveBucket honours a caller's bucket but clamps it so no request can ask
// the server to build an unbounded number of buckets.
func resolveBucket(r *http.Request, from, to int64) int64 {
	span := to - from
	if span <= 0 {
		span = 1
	}

	bucket := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("bucket")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			bucket = d.Milliseconds()
		}
	}
	if bucket <= 0 {
		bucket = span / 240 // ~240 buckets across the window by default
	}
	if min := span / maxTimelineBuckets; bucket < min {
		bucket = min
	}
	if bucket <= 0 {
		bucket = 1
	}
	return bucket
}

// --- ingest ---

// ingestRequest is deliberately loose so Uptime Kuma, a cron curl and a Home
// Assistant automation all work without a custom integration.
type ingestRequest struct {
	Type     string `json:"type"`
	Project  string `json:"project"`
	Service  string `json:"service"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Actor    string `json:"actor"`
	TS       int64  `json:"ts"`
}

// maxIngestBody caps what an unauthenticated caller can make Silt buffer.
const maxIngestBody = 64 << 10

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	// Fail closed: an unset token means the endpoint is not configured, not
	// that it is open.
	if s.conf().IngestToken == "" {
		writeError(w, http.StatusServiceUnavailable, "ingest is not configured; set SILT_INGEST_TOKEN")
		return
	}
	if !s.ingestAuthorised(r) {
		writeError(w, http.StatusUnauthorized, "invalid or missing token")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxIngestBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	if len(body) > maxIngestBody {
		writeError(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}

	var req ingestRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}

	rec := store.EventRecord{
		Service:  req.Service,
		TS:       req.TS,
		Source:   store.SourceWebhook,
		Type:     req.Type,
		Severity: normaliseSeverity(req.Severity),
		Actor:    req.Actor,
		Message:  req.Message,
	}

	// Attach to a known project so external events land beside the changes
	// they may explain. An explicit project name wins; otherwise fall back to
	// the service name, since monitors are usually named after a service
	// rather than its project. Unmatched events are still recorded at host
	// level rather than dropped.
	if id, ok := s.resolveProject(r, req.Project, req.Service); ok {
		rec.ProjectID = &id
	}

	row, err := s.store.RecordEvent(r.Context(), rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "record event")
		return
	}
	s.hub.Publish(Message{Event: "event", Data: toEventResponse(row)})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": row.ID})
}

// ingestAuthorised accepts the token as a bearer header or a query parameter.
// Not every webhook source can set custom headers, and a webhook nobody can
// call is not a feature.
func (s *Server) ingestAuthorised(r *http.Request) bool {
	// Read once: the token is editable at runtime, and comparing the header
	// against one value and the query against another would be a race with a
	// security answer.
	token := s.conf().IngestToken
	if token == "" {
		return false
	}
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		if constantTimeEqual(strings.TrimPrefix(header, "Bearer "), token) {
			return true
		}
	}
	if query := r.URL.Query().Get("token"); query != "" {
		return constantTimeEqual(query, token)
	}
	return false
}

// resolveProject maps an ingested event to a project by project name first,
// then by service name.
func (s *Server) resolveProject(r *http.Request, project, service string) (int64, bool) {
	if strings.TrimSpace(project) != "" {
		if id, ok := s.findProjectByName(r, project); ok {
			return id, true
		}
	}
	if strings.TrimSpace(service) != "" {
		if id, ok := s.findProjectByName(r, service); ok {
			return id, true
		}
		if id, err := s.store.RQ.ProjectForService(r.Context(), service); err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

func (s *Server) findProjectByName(r *http.Request, name string) (int64, bool) {
	hosts, err := s.store.RQ.ListHosts(r.Context())
	if err != nil {
		return 0, false
	}
	for _, h := range hosts {
		projects, err := s.store.RQ.ListProjects(r.Context(), h.ID)
		if err != nil {
			continue
		}
		for _, p := range projects {
			if strings.EqualFold(p.Name, name) {
				return p.ID, true
			}
		}
	}
	return 0, false
}

// --- mapping ---

func toProjectResponse(p sqlcgen.Project) projectResponse {
	files := []string{}
	_ = json.Unmarshal([]byte(p.ConfigFiles), &files)
	return projectResponse{
		ID:          p.ID,
		HostID:      p.HostID,
		Name:        p.Name,
		WorkingDir:  p.WorkingDir,
		ConfigFiles: files,
		FirstSeenAt: p.FirstSeenAt,
		LastSeenAt:  p.LastSeenAt,
		Archived:    p.Archived == 1,
	}
}

func toSnapshotResponse(s sqlcgen.Snapshot) snapshotResponse {
	return snapshotResponse{
		ID:               s.ID,
		ProjectID:        s.ProjectID,
		TakenAt:          s.TakenAt,
		LastObservedAt:   s.LastObservedAt,
		ObservationCount: s.ObservationCount,
		Trigger:          s.Trigger,
		ComposeSource:    s.ComposeSource,
		ConfigChanged:    s.ConfigChanged == 1,
		RuntimeChanged:   s.RuntimeChanged == 1,
	}
}

func toEventResponse(e sqlcgen.Event) eventResponse {
	out := eventResponse{
		ID:       e.ID,
		Service:  e.Service,
		TS:       e.Ts,
		Source:   e.Source,
		Type:     e.Type,
		Severity: e.Severity,
		Actor:    e.Actor,
		Message:  e.Message,
	}
	if e.ProjectID.Valid {
		id := e.ProjectID.Int64
		out.ProjectID = &id
	}
	if json.Valid([]byte(e.Payload)) && e.Payload != "" {
		out.Payload = json.RawMessage(e.Payload)
	}
	return out
}

func toEventResponses(rows []sqlcgen.Event) []eventResponse {
	out := make([]eventResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEventResponse(e))
	}
	return out
}

func toDiffInput(m store.SnapshotModel) diff.Input {
	runtimes := make(map[string]diff.Runtime, len(m.Runtimes))
	for name, rt := range m.Runtimes {
		runtimes[name] = diff.Runtime{
			State:        rt.State,
			Health:       rt.Health,
			RestartCount: rt.RestartCount,
		}
	}
	return diff.Input{
		Side:     diff.Side{SnapshotID: m.Snapshot.ID, TakenAt: m.Snapshot.TakenAt},
		Project:  m.Project,
		Runtimes: runtimes,
	}
}

func writeSnapshotLoadError(w http.ResponseWriter, err error, id int64) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "read snapshot")
}

func normaliseSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case store.SeverityWarn, "warning":
		return store.SeverityWarn
	case store.SeverityError, "critical", "fatal", "down":
		return store.SeverityError
	default:
		return store.SeverityInfo
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
