package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

func openTestStore(t *testing.T) (*store.Store, *redact.Redactor) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := db.RedactionKey(context.Background())
	if err != nil {
		t.Fatalf("redaction key: %v", err)
	}
	return db, redact.New(key, nil)
}

type serviceOpts struct {
	image        string
	imageID      string
	env          []string
	state        string
	health       string
	restartCount int
	startedAt    int64
}

func observation(t *testing.T, r *redact.Redactor, opts serviceOpts) compose.Observation {
	t.Helper()
	if opts.image == "" {
		opts.image = "example/app:latest"
	}
	if opts.imageID == "" {
		opts.imageID = "sha256:aaaa"
	}
	if opts.state == "" {
		opts.state = "running"
	}
	started := opts.startedAt
	obs, err := compose.Build(
		docker.Project{Name: "media"},
		[]compose.ServiceInput{{
			Service: "app",
			Inspected: docker.Inspected{
				Config: docker.ContainerConfig{
					Image:   opts.image,
					ImageID: opts.imageID,
					Env:     opts.env,
				},
				Runtime: docker.RuntimeState{
					ContainerID:  "c1",
					State:        opts.state,
					Health:       opts.health,
					RestartCount: opts.restartCount,
					StartedAt:    &started,
				},
			},
		}},
		r,
	)
	if err != nil {
		t.Fatalf("build observation: %v", err)
	}
	return obs
}

func writeSnap(t *testing.T, db *store.Store, projectID int64, obs compose.Observation) store.SnapshotResult {
	t.Helper()
	res, err := db.WriteSnapshot(context.Background(), projectID, store.Now(), "manual", obs)
	if err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return res
}

func newProject(t *testing.T, db *store.Store) int64 {
	t.Helper()
	_, id, err := db.UpsertHostAndProject(context.Background(), "local", "tcp://proxy:2375", "28.0", testProject{name: "media"})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	return id
}

// The first observation of a project is a change by definition.
func TestFirstSnapshotIsChanged(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	res := writeSnap(t, db, id, observation(t, r, serviceOpts{}))
	if !res.ConfigChanged || !res.RuntimeChanged {
		t.Errorf("first snapshot: config=%v runtime=%v, want both true", res.ConfigChanged, res.RuntimeChanged)
	}
}

// An identical observation must register no change at all. This is what makes
// interval snapshotting nearly free.
func TestIdenticalObservationIsUnchanged(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	opts := serviceOpts{startedAt: 1700000000000}

	writeSnap(t, db, id, observation(t, r, opts))
	res := writeSnap(t, db, id, observation(t, r, opts))

	if res.ConfigChanged {
		t.Error("identical observation reported a config change")
	}
	if res.RuntimeChanged {
		t.Error("identical observation reported a runtime change")
	}
}

// M2's done-criterion, and the reason the fingerprint is split: a restart must
// not earn the long retention tier or fire a notification.
func TestRestartIsRuntimeChangeOnly(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	writeSnap(t, db, id, observation(t, r, serviceOpts{startedAt: 1700000000000}))
	res := writeSnap(t, db, id, observation(t, r, serviceOpts{
		startedAt:    1700000900000, // restarted
		restartCount: 1,
	}))

	if res.ConfigChanged {
		t.Error("a restart reported a CONFIG change; the fingerprint split is defeated")
	}
	if !res.RuntimeChanged {
		t.Error("a restart reported no runtime change")
	}
}

// A health transition is runtime-only for the same reason.
func TestHealthTransitionIsRuntimeChangeOnly(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	writeSnap(t, db, id, observation(t, r, serviceOpts{health: "healthy", startedAt: 1700000000000}))
	res := writeSnap(t, db, id, observation(t, r, serviceOpts{health: "unhealthy", startedAt: 1700000000000}))

	if res.ConfigChanged {
		t.Error("a health transition reported a config change")
	}
	if !res.RuntimeChanged {
		t.Error("a health transition reported no runtime change")
	}
}

// The other half of the criterion: a new image is a config change.
func TestNewImageIsConfigChange(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:aaaa", startedAt: 1700000000000}))
	res := writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:bbbb", startedAt: 1700000000000}))

	if !res.ConfigChanged {
		t.Error("a new image ID did not report a config change; this is the case Silt exists for")
	}
}

// An env value changing is a config change even though the value is redacted:
// the HMAC digest differs, so the compose blob differs.
func TestChangedEnvValueIsConfigChange(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)

	writeSnap(t, db, id, observation(t, r, serviceOpts{env: []string{"SECRET=one"}, startedAt: 1700000000000}))
	res := writeSnap(t, db, id, observation(t, r, serviceOpts{env: []string{"SECRET=two"}, startedAt: 1700000000000}))

	if !res.ConfigChanged {
		t.Error("a changed secret value did not register; redaction must not hide the fact of a change")
	}
}

// Identical content must be stored once. This is what keeps 40 services
// snapshotted every 5 minutes from filling the disk.
func TestBlobsAreDeduplicated(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	ctx := context.Background()
	opts := serviceOpts{startedAt: 1700000000000}

	writeSnap(t, db, id, observation(t, r, opts))
	after1, err := db.Usage(ctx)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}

	for i := 0; i < 20; i++ {
		writeSnap(t, db, id, observation(t, r, opts))
	}
	after21, err := db.Usage(ctx)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}

	if after21.Blobs != after1.Blobs {
		t.Errorf("blob count grew from %d to %d across 20 identical snapshots; dedupe is not working",
			after1.Blobs, after21.Blobs)
	}
}

func TestBlobRoundTrip(t *testing.T) {
	db, _ := openTestStore(t)
	ctx := context.Background()

	content := []byte(strings.Repeat("silt ", 1000))
	hash, err := db.PutBlob(ctx, nil, content)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := db.GetBlob(ctx, hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(content) {
		t.Error("blob did not round-trip through zstd")
	}
	if hash != store.Hash(content) {
		t.Error("hash is not the address of the uncompressed content")
	}
}

// With no-change observations now touching the previous snapshot instead of
// inserting, config_changed = 0 means "runtime changed but configuration did
// not" — a restart. Those are the rows a crash-looping container produces in
// bulk, and they prune on the short tier while real config changes are kept.
func TestPruneDropsRuntimeOnlySnapshotsAndKeepsConfigChanges(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	ctx := context.Background()

	// Two genuine configuration changes.
	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:aaaa", startedAt: 1}))
	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:bbbb", startedAt: 1}))

	// A container flapping: same config, restarting over and over.
	for i := 0; i < 5; i++ {
		writeSnap(t, db, id, observation(t, r, serviceOpts{
			imageID:      "sha256:bbbb",
			startedAt:    int64(1000 + i),
			restartCount: i + 1,
		}))
	}

	future := time.Now().Add(48 * time.Hour)
	stats, err := db.Prune(ctx, store.RetentionPolicy{Unchanged: time.Hour}, future)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if stats.UnchangedSnapshots != 5 {
		t.Errorf("pruned %d runtime-only snapshots, want 5", stats.UnchangedSnapshots)
	}
	if stats.ChangedSnapshots != 0 {
		t.Errorf("pruned %d config-changed snapshots, want 0", stats.ChangedSnapshots)
	}

	remaining, err := db.RQ.CountSnapshots(ctx, id)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 2 {
		t.Errorf("%d snapshots remain, want the 2 configuration changes", remaining)
	}
}

// An observation identical to the last one must not insert a row at all.
func TestUnchangedObservationTouchesInsteadOfInserting(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	ctx := context.Background()
	opts := serviceOpts{startedAt: 1700000000000}

	first := writeSnap(t, db, id, observation(t, r, opts))
	for i := 0; i < 10; i++ {
		res := writeSnap(t, db, id, observation(t, r, opts))
		if !res.Touched {
			t.Fatalf("observation %d inserted a row instead of touching", i)
		}
		if res.ID != first.ID {
			t.Fatalf("touch returned snapshot %d, want the existing %d", res.ID, first.ID)
		}
	}

	count, err := db.RQ.CountSnapshots(ctx, id)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("%d snapshots after 11 identical observations, want 1", count)
	}

	snap, err := db.RQ.GetSnapshot(ctx, first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if snap.ObservationCount != 11 {
		t.Errorf("observation_count = %d, want 11", snap.ObservationCount)
	}
	if snap.LastObservedAt <= snap.TakenAt {
		t.Error("last_observed_at was not advanced")
	}
}

// The oldest snapshot is the base for the earliest diff and must survive any
// retention pass.
func TestPruneNeverRemovesOldestSnapshot(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:aaaa", startedAt: 1}))
	}

	future := time.Now().Add(1000 * time.Hour)
	if _, err := db.Prune(ctx, store.RetentionPolicy{
		Changed:   time.Hour,
		Unchanged: time.Hour,
		Events:    time.Hour,
	}, future); err != nil {
		t.Fatalf("prune: %v", err)
	}

	remaining, err := db.RQ.CountSnapshots(ctx, id)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 1 {
		t.Errorf("after pruning everything, %d snapshots remain; want exactly the oldest", remaining)
	}
}

// GC must collect blobs the pruned snapshots referenced — and must walk
// service_states.inspect_hash, not just snapshots.compose_hash.
func TestGarbageCollectsOrphanedBlobs(t *testing.T) {
	db, r := openTestStore(t)
	id := newProject(t, db)
	ctx := context.Background()

	writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:aaaa", startedAt: 1}))
	for i := 0; i < 4; i++ {
		writeSnap(t, db, id, observation(t, r, serviceOpts{imageID: "sha256:" + strings.Repeat("b", i+1), startedAt: 1}))
	}

	before, _ := db.Usage(ctx)
	if before.Blobs < 4 {
		t.Fatalf("expected several blobs before GC, got %d", before.Blobs)
	}

	future := time.Now().Add(1000 * time.Hour)
	stats, err := db.Prune(ctx, store.RetentionPolicy{Changed: time.Hour, Unchanged: time.Hour}, future)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if stats.Blobs == 0 {
		t.Error("prune deleted snapshots but collected no blobs")
	}

	after, _ := db.Usage(ctx)
	if after.Blobs >= before.Blobs {
		t.Errorf("blobs went from %d to %d; GC collected nothing", before.Blobs, after.Blobs)
	}
}
