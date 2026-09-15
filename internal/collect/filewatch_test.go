package collect_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/unmaykr-a/silt/internal/collect"
	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker/dockertest"
)

// The third trigger from PROJECT.md Section 5, which was specified, named in
// the locked tech stack and in the repo layout, given a gotcha of its own, and
// never built. These tests are what makes it real rather than documented.

// watched sets up a project whose compose file lives in a temporary directory,
// and returns the watcher's notifications.
func watched(t *testing.T, debounce time.Duration) (*harness, string, <-chan string) {
	t.Helper()
	h := newHarness(t)

	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "compose.yaml")
	write(t, path, "services:\n  radarr:\n    image: radarr:5.4.0\n")

	// The container's labels are where the path comes from, exactly as on a
	// real host.
	h.engine.SetContainers([]container.Summary{
		dockertest.Container("media-radarr", "media", "radarr", "radarr:5.4.0", dir),
	})
	h.engine.SetInspect("media-radarr",
		dockertest.Inspect("media-radarr", "media-radarr-1", "radarr:5.4.0", "sha256:aaa", nil))

	changed := make(chan string, 8)
	fw := &collect.FileWatcher{
		Client:   h.client,
		Files:    &compose.FileReader{Roots: []string{root}, MaxBytes: 1 << 20},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Debounce: debounce,
		Refresh:  50 * time.Millisecond,
		OnChange: func(project string) { changed <- project },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = fw.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the file watcher did not stop when its context was cancelled")
		}
	})

	// Let the first rebuild add the watch before anything is written.
	time.Sleep(150 * time.Millisecond)
	return h, path, changed
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expectChange(t *testing.T, changed <-chan string, want string) {
	t.Helper()
	select {
	case got := <-changed:
		if got != want {
			t.Errorf("snapshotted %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no snapshot for %s after editing its compose file", want)
	}
}

func expectNoChange(t *testing.T, changed <-chan string, why string) {
	t.Helper()
	select {
	case got := <-changed:
		t.Errorf("%s produced a snapshot of %q", why, got)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestEditingAComposeFileSnapshotsItsProject(t *testing.T) {
	_, path, changed := watched(t, 50*time.Millisecond)
	write(t, path, "services:\n  radarr:\n    image: radarr:5.6.0\n")
	expectChange(t, changed, "media")
}

func TestAnAtomicSaveIsStillNoticed(t *testing.T) {
	// The gotcha this design exists for. An editor that writes a new file and
	// renames it over the old one replaces the inode, so a watch on the file
	// goes deaf after the first save while continuing to report success — which
	// is why the watch is on the directory.
	_, path, changed := watched(t, 50*time.Millisecond)

	for i, image := range []string{"radarr:5.5.0", "radarr:5.6.0", "radarr:5.7.0"} {
		tmp := path + ".tmp"
		write(t, tmp, "services:\n  radarr:\n    image: "+image+"\n")
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
		// Every save, not just the first: the point is that it keeps working.
		expectChange(t, changed, "media")
		_ = i
	}
}

func TestABurstOfWritesIsOneSnapshot(t *testing.T) {
	// A save is not one write. Truncate, several writes and a rename is normal,
	// and vim's default produces four events in a few milliseconds.
	_, path, changed := watched(t, 300*time.Millisecond)

	for i := 0; i < 5; i++ {
		write(t, path, "services:\n  radarr:\n    image: radarr:5.6.0\n# "+string(rune('a'+i))+"\n")
		time.Sleep(20 * time.Millisecond)
	}
	expectChange(t, changed, "media")
	expectNoChange(t, changed, "a single burst of writes")
}

func TestAnUnrelatedFileInTheSameDirectoryIsIgnored(t *testing.T) {
	// A compose directory holds data directories, lock files and editor swap
	// files. Snapshotting on a container's own writes would make Silt a source
	// of the churn it is recording.
	_, path, changed := watched(t, 50*time.Millisecond)

	write(t, filepath.Join(filepath.Dir(path), "radarr.db"), "not a compose file")
	write(t, filepath.Join(filepath.Dir(path), ".compose.yaml.swp"), "vim")
	expectNoChange(t, changed, "writing a file the project does not declare")

	// And the declared one still works, so the filter is not simply off.
	write(t, path, "services:\n  radarr:\n    image: radarr:5.6.0\n")
	expectChange(t, changed, "media")
}

func TestAFileOutsideTheComposeRootsIsNotWatched(t *testing.T) {
	// The paths come from container labels, and anyone who can start a
	// container can set those. The watch applies the same allowlist the reader
	// does — otherwise a crafted label would have Silt watching anywhere.
	h := newHarness(t)

	outside := t.TempDir()
	path := filepath.Join(outside, "compose.yaml")
	write(t, path, "services: {}\n")

	h.engine.SetContainers([]container.Summary{
		dockertest.Container("evil-app", "evil", "app", "app:1", outside),
	})
	h.engine.SetInspect("evil-app",
		dockertest.Inspect("evil-app", "evil-app-1", "app:1", "sha256:bbb", nil))

	changed := make(chan string, 4)
	fw := &collect.FileWatcher{
		Client: h.client,
		// A root that does not contain the labelled path.
		Files:    &compose.FileReader{Roots: []string{t.TempDir()}, MaxBytes: 1 << 20},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Debounce: 50 * time.Millisecond,
		Refresh:  50 * time.Millisecond,
		OnChange: func(project string) { changed <- project },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fw.Run(ctx) }()
	time.Sleep(150 * time.Millisecond)

	write(t, path, "services:\n  app:\n    image: app:2\n")
	expectNoChange(t, changed, "editing a file outside the configured roots")
}

func TestWithNoComposeRootsTheWatchIsSimplyOff(t *testing.T) {
	// File capture is opt-in, so nothing to watch is not an error.
	h := newHarness(t)
	fw := &collect.FileWatcher{
		Client:   h.client,
		Files:    &compose.FileReader{},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnChange: func(string) { t.Error("something was watched with no roots configured") },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := fw.Run(ctx); err == nil {
		t.Error("Run returned before its context was done")
	}
}

func TestTheCollectorSnapshotsOnAComposeFileEdit(t *testing.T) {
	// Through the Collector rather than the watcher alone: the wiring is the
	// part that was missing, and a watcher nothing calls is the same as no
	// watcher. Asserts the snapshot lands with the `file` trigger, which the
	// schema and the OpenAPI enum have always declared and nothing produced.
	h := newHarness(t)

	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "compose.yaml")
	write(t, path, "services:\n  radarr:\n    image: radarr:5.4.0\n")

	h.engine.SetContainers([]container.Summary{
		dockertest.Container("media-radarr", "media", "radarr", "radarr:5.4.0", dir),
	})
	h.engine.SetInspect("media-radarr",
		dockertest.Inspect("media-radarr", "media-radarr-1", "radarr:5.4.0", "sha256:aaa", nil))

	// Both halves get the same reader, as main.go wires them: the watcher
	// decides when to snapshot and the snapshotter decides what to capture, and
	// with files missing from the second the observation is identical to the
	// last one — so the edit produces a touch rather than a row, and nothing to
	// assert on.
	reader := &compose.FileReader{Roots: []string{root}, MaxBytes: 1 << 20, Redactor: h.snap.Redactor}
	h.snap.Files = reader

	c := collector(h)
	c.Files = reader
	c.FileDebounce = 50 * time.Millisecond

	runFor(t, c, func() {
		waitFor(t, "the watcher to subscribe", func() bool { return h.engine.Subscriptions() > 0 })
		// A real edit, so the files fingerprint actually moves.
		write(t, path, "services:\n  radarr:\n    image: radarr:5.6.0\n")
		waitFor(t, "a snapshot triggered by the file edit", func() bool {
			for _, s := range snapshots(t, h.db) {
				if s.Trigger == collect.TriggerFile {
					return true
				}
			}
			return false
		})
	})
}
