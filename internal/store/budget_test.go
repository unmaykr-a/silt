package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

// realisticService produces a service with the shape of an actual homelab
// container: a dozen env vars, several mounts, labels, ports.
func realisticService(n int) compose.ServiceInput {
	name := fmt.Sprintf("service%02d", n)
	return compose.ServiceInput{
		Service: name,
		Inspected: docker.Inspected{
			Config: docker.ContainerConfig{
				Image:   fmt.Sprintf("lscr.io/linuxserver/%s:latest", name),
				ImageID: fmt.Sprintf("sha256:%064x", n),
				Env: []string{
					"PUID=1000", "PGID=1000", "TZ=Europe/Tallinn", "UMASK=022",
					"LOG_LEVEL=info", "APP_PORT=8080",
					fmt.Sprintf("API_KEY=secret-value-for-%s", name),
					fmt.Sprintf("DB_PASSWORD=another-secret-%s", name),
					"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
					"LANG=en_US.UTF-8", "HOME=/root", "TERM=xterm",
				},
				Cmd: []string{"/init"},
				Labels: map[string]string{
					"com.docker.compose.project":     "media",
					"com.docker.compose.service":     name,
					"com.docker.compose.config-hash": fmt.Sprintf("%064x", n),
					"org.opencontainers.image.title": name,
				},
				Mounts: []docker.Mount{
					{Type: "bind", Source: "/srv/media/" + name + "/config", Target: "/config", Mode: "rw"},
					{Type: "bind", Source: "/mnt/storage/media", Target: "/data", Mode: "rw"},
				},
				PortBindings:  []string{fmt.Sprintf("0.0.0.0:%d->8080/tcp", 8000+n)},
				ExposedPorts:  []string{"8080/tcp"},
				Networks:      []string{"media_default"},
				RestartPolicy: "unless-stopped",
				Healthcheck:   []string{"CMD-SHELL", "curl -f http://localhost:8080/ping || exit 1"},
			},
			Runtime: docker.RuntimeState{
				ContainerID:   fmt.Sprintf("container%02d", n),
				ContainerName: "media-" + name + "-1",
				State:         "running",
				Health:        "healthy",
			},
		},
	}
}

// M2's storage budget: an idle hour of interval snapshots over 40 services
// must cost almost nothing, because dedupe means an unchanged stack stores no
// new blobs at all. The brief's figure is "under ~50 KB".
func TestIdleHourStaysUnderSizeBudget(t *testing.T) {
	const (
		services         = 40
		snapshotsPerHour = 12 // one every 5 minutes
		budgetBytes      = 50 * 1024
	)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "silt.db")
	ctx := context.Background()

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	key, _ := db.RedactionKey(ctx)
	r := redact.New(key, nil)

	inputs := make([]compose.ServiceInput, 0, services)
	for i := 0; i < services; i++ {
		inputs = append(inputs, realisticService(i))
	}
	obs, err := compose.Build(docker.Project{Name: "media", WorkingDir: "/srv/media"}, inputs, r)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	_, projectID, err := db.UpsertHostAndProject(ctx, "local", "tcp://proxy:2375", "28.0", testProject{name: "media"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The first snapshot is the baseline cost, which the budget is not about.
	if _, err := db.WriteSnapshot(ctx, projectID, store.Now(), "interval", obs); err != nil {
		t.Fatalf("baseline snapshot: %v", err)
	}
	if err := checkpoint(ctx, dbPath); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	baseline, err := fileSize(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	usageBefore, _ := db.Usage(ctx)

	// An idle hour: the same observation, over and over.
	for i := 1; i < snapshotsPerHour; i++ {
		if _, err := db.WriteSnapshot(ctx, projectID, store.Now(), "interval", obs); err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
	}
	if err := checkpoint(ctx, dbPath); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	after, err := fileSize(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	usageAfter, _ := db.Usage(ctx)

	growth := after - baseline
	t.Logf("40 services x %d idle snapshots: baseline %d B, growth %d B (%.1f KB), blobs %d -> %d",
		snapshotsPerHour, baseline, growth, float64(growth)/1024, usageBefore.Blobs, usageAfter.Blobs)

	if usageAfter.Blobs != usageBefore.Blobs {
		t.Errorf("blob count grew from %d to %d over an idle hour; dedupe is not holding",
			usageBefore.Blobs, usageAfter.Blobs)
	}
	if growth > budgetBytes {
		t.Errorf("idle hour grew the database by %d bytes, over the %d byte budget", growth, budgetBytes)
	}
}

func fileSize(path string) (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		info, err := os.Stat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}
