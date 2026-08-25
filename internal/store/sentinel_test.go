package store_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

// sentinel is planted in every secret-shaped field. It must appear nowhere in
// anything Silt persists or logs.
const sentinel = "SILT-SENTINEL-d41d8cd98f00b204e9800998ecf8427e"

type testProject struct {
	name  string
	files []string
}

func (p testProject) ProjectName() string       { return p.name }
func (p testProject) ProjectWorkingDir() string { return "/srv/" + p.name }
func (p testProject) ConfigFiles() []string     { return p.files }

// TestSentinelNeverReachesDisk is M2's real gate.
//
// It plants a known string in every field a secret could plausibly occupy,
// runs a full snapshot write plus prune and GC, then byte-scans the database
// file, its WAL, every decompressed blob, and captured log output. Byte
// scanning rather than querying is the point: a leak through an unexpected
// column or a compressed blob would pass a query-based check.
func TestSentinelNeverReachesDisk(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "silt.db")
	ctx := context.Background()

	// Capture everything logged at debug level while the snapshot is built.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = db.Close() }()

	key, err := db.RedactionKey(ctx)
	if err != nil {
		t.Fatalf("redaction key: %v", err)
	}
	r := redact.New(key, nil)

	inspected := docker.Inspected{
		Config: docker.ContainerConfig{
			Image:   "example/app:latest",
			ImageID: "sha256:aaaa",
			Env: []string{
				"POSTGRES_PASSWORD=" + sentinel,
				"API_TOKEN=" + sentinel,
				"PW=" + sentinel,
				"SMTP_LOGIN=" + sentinel,
				"MY_UNGUESSABLE_KEY_NAME=" + sentinel,
				"PUID=1000",
			},
			Cmd:        []string{"serve", "--token=" + sentinel},
			Entrypoint: []string{"/entry.sh", "--secret=" + sentinel},
			Labels: map[string]string{
				"com.docker.compose.project":                    "media",
				"com.docker.compose.service":                    "app",
				"traefik.http.middlewares.auth.basicauth.users": sentinel,
			},
			Mounts: []docker.Mount{
				{Type: "bind", Source: "/srv/secrets/" + sentinel, Target: "/run/secret", Mode: "ro"},
				{Type: "volume", Source: "media_config", Target: "/config", Mode: "rw"},
			},
		},
		Runtime: docker.RuntimeState{
			ContainerID:   "c1",
			ContainerName: "media-app-1",
			State:         "running",
			Health:        "healthy",
		},
	}

	obs, err := compose.Build(
		docker.Project{Name: "media", WorkingDir: "/srv/media"},
		[]compose.ServiceInput{{Service: "app", Inspected: inspected}},
		r,
	)
	if err != nil {
		t.Fatalf("build model: %v", err)
	}

	// Log the model the way production code would, to prove the redacted
	// model is what reaches slog.
	logger.Debug("built observation", "project", obs.Project.Name, "services", obs.Project.Services)

	_, projectID, err := db.UpsertHostAndProject(ctx, "local", "tcp://proxy:2375", "28.0", testProject{name: "media"})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if _, err := db.WriteSnapshot(ctx, projectID, store.Now(), "manual", obs); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	// Exercise prune and GC too: a leak could hide in a path only they touch.
	if _, err := db.Prune(ctx, store.RetentionPolicy{Changed: 0, Unchanged: 0, Events: 0}, timeNow()); err != nil {
		t.Fatalf("prune: %v", err)
	}

	// Force WAL content into the main database file as well, so the scan
	// covers both regardless of checkpoint timing.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	scanned := 0
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := dbPath + suffix
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		scanned++
		if bytes.Contains(data, []byte(sentinel)) {
			t.Errorf("sentinel found in %s (%d bytes)", filepath.Base(path), len(data))
		}
	}
	if scanned == 0 {
		t.Fatal("no database files were scanned; the test proved nothing")
	}

	if strings.Contains(logBuf.String(), sentinel) {
		t.Errorf("sentinel found in log output:\n%s", logBuf.String())
	}

	// And re-open to walk every blob decompressed, since the on-disk bytes
	// are zstd-compressed and a scan of the raw file cannot see through them.
	db2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	hashes, err := allBlobHashes(ctx, dbPath)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}
	if len(hashes) == 0 {
		t.Fatal("no blobs stored; the decompressed scan proved nothing")
	}
	for _, h := range hashes {
		content, err := db2.GetBlob(ctx, h)
		if err != nil {
			t.Fatalf("read blob %s: %v", h, err)
		}
		if bytes.Contains(content, []byte(sentinel)) {
			t.Errorf("sentinel found in decompressed blob %s:\n%s", h[:12], content)
		}
	}
}

// TestKeptValuesSurvive guards the opposite failure: redacting everything
// would pass the sentinel test and make Silt useless.
func TestKeptValuesSurvive(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(dir, "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	key, _ := db.RedactionKey(ctx)
	r := redact.New(key, nil)

	obs, err := compose.Build(
		docker.Project{Name: "media"},
		[]compose.ServiceInput{{Service: "app", Inspected: docker.Inspected{
			Config: docker.ContainerConfig{
				Image: "example/app:latest",
				Env:   []string{"PUID=1000", "TZ=Europe/Tallinn", "SECRET=nope"},
			},
		}}},
		r,
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	env := obs.Project.Services["app"].Environment
	if env["PUID"] != "1000" {
		t.Errorf("PUID = %q, want 1000 readable", env["PUID"])
	}
	if env["TZ"] != "Europe/Tallinn" {
		t.Errorf("TZ = %q, want readable", env["TZ"])
	}
	if env["SECRET"] == "nope" {
		t.Error("SECRET was kept in cleartext")
	}
}
