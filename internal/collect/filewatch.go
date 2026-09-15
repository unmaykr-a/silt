package collect

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
)

// Watching the compose files themselves.
//
// The third of the four triggers in PROJECT.md Section 5, and the one that was
// specified, named in the locked tech stack, given a gotcha of its own, and
// never built. Without it an edit is noticed on the next Docker event or the
// next interval reconcile — so up to five minutes on the default cadence, and
// indefinitely on a host where nothing else is happening, which is exactly the
// host where an un-applied edit is easiest to forget about.
//
// That matters because "you edited this and never applied it" is one of the
// things Silt is for. A question answered within a second reads as Silt
// noticing; the same answer four minutes later reads as Silt being asked.

// DefaultFileDebounce is how long to wait after a write before snapshotting.
//
// Editors do not write once. A save is often a truncate, several writes and a
// rename, and vim's default is to write a new file and move it over the old
// one — so a single ^S can produce four events in a few milliseconds.
const DefaultFileDebounce = time.Second

// FileWatcher snapshots a project when its compose files change on disk.
type FileWatcher struct {
	Client *docker.Client
	// Files supplies the root allowlist. A path outside it is not watched, for
	// the same reason it is not read: the paths come from container labels, and
	// anyone who can start a container can set those.
	Files *compose.FileReader
	Log   *slog.Logger
	// Debounce is the quiet period after a write. Zero means the default.
	Debounce time.Duration
	// OnChange is called with a project name once its files have settled.
	OnChange func(project string)
	// Refresh is how often the watched set is rebuilt from the running
	// projects. Zero means the default.
	Refresh time.Duration

	// dirs is what is currently watched, keyed by directory. files is which
	// paths inside them are a project's declared compose files.
	mu    sync.Mutex
	dirs  map[string]string
	files map[string]string
}

// DefaultWatchRefresh is how often the watched directory set is rebuilt.
//
// Projects come and go, so the set is not static. Rebuilding it on a timer
// rather than reacting to Docker events keeps this independent of the event
// stream: a host whose daemon has gone quiet is one where a file edit is the
// only thing left to notice.
const DefaultWatchRefresh = time.Minute

// Run blocks until ctx is cancelled.
func (w *FileWatcher) Run(ctx context.Context) error {
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	if !w.Files.Enabled() {
		// Nothing is captured from disk, so there is nothing to watch. Not an
		// error: file capture is opt-in.
		<-ctx.Done()
		return ctx.Err()
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	d := &debouncer{
		wait: w.Debounce,
		fire: w.OnChange,
	}
	if d.wait <= 0 {
		d.wait = DefaultFileDebounce
	}
	defer d.stop()

	// Two maps, both rebuilt together.
	//
	// dirs is what is watched: the directory rather than the file, which is the
	// gotcha Section 12 records — an atomic-save editor replaces the inode, and
	// a watch on the old one goes deaf after the first save while continuing to
	// report success.
	//
	// files is which paths inside those directories matter, so an event is a
	// map lookup. A compose directory also holds data directories, lock files
	// and editor swap files, and asking Docker on every write would make Silt
	// answer a filesystem event with an API call.
	w.dirs = map[string]string{}
	w.files = map[string]string{}

	refresh := w.Refresh
	if refresh <= 0 {
		refresh = DefaultWatchRefresh
	}
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()

	w.rebuild(ctx, watcher, log)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			w.rebuild(ctx, watcher, log)

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Chmod alone is not a content change, and some editors touch
			// permissions on save. Everything else — write, create, rename,
			// remove — can change what Silt would capture.
			if event.Op == fsnotify.Chmod {
				continue
			}
			// Only the files a project actually declares. Snapshotting on a
			// container's own writes would make Silt a source of the churn it
			// is recording.
			project, declared := w.declaredFor(event.Name)
			if !declared {
				continue
			}
			log.Debug("compose file changed", "project", project, "path", event.Name, "op", event.Op.String())
			d.touch(project)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// A watch error is not fatal: the interval reconcile is still
			// running underneath this, so the worst case is the latency this
			// exists to remove.
			log.Warn("compose file watch error", "error", err)
		}
	}
}

// rebuild brings the watched set in line with what is running.
func (w *FileWatcher) rebuild(ctx context.Context, watcher *fsnotify.Watcher, log *slog.Logger) {
	projects, err := w.Client.Discover(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("could not discover projects to watch", "error", err)
		}
		return
	}

	wantDirs := map[string]string{}
	wantFiles := map[string]string{}
	for _, p := range projects {
		for _, path := range p.ConfigFiles {
			clean := filepath.Clean(path)
			dir := filepath.Dir(clean)
			// The root check, for the same reason the reader applies it: these
			// paths come from container labels, and anyone who can start a
			// container can set those.
			if !w.Files.UnderRoot(dir) {
				continue
			}
			wantDirs[dir] = p.Name
			wantFiles[clean] = p.Name
		}
	}

	w.mu.Lock()
	w.files = wantFiles
	w.mu.Unlock()

	for dir := range w.dirs {
		if _, keep := wantDirs[dir]; !keep {
			_ = watcher.Remove(dir)
			delete(w.dirs, dir)
		}
	}
	for dir, project := range wantDirs {
		if _, have := w.dirs[dir]; have {
			w.dirs[dir] = project
			continue
		}
		if err := watcher.Add(dir); err != nil {
			// A directory that is configured but not mounted is the common
			// shape here, and the probes on the settings screen already report
			// it. Warn rather than failing: the interval reconcile still runs.
			log.Warn("could not watch compose directory", "dir", dir, "error", err)
			continue
		}
		w.dirs[dir] = project
		log.Debug("watching compose directory", "dir", dir, "project", project)
	}
}

// declaredFor returns the project a changed path belongs to, if any.
func (w *FileWatcher) declaredFor(path string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	project, ok := w.files[filepath.Clean(path)]
	return project, ok
}

// debouncer collapses a burst of writes per project into one call.
type debouncer struct {
	mu     sync.Mutex
	wait   time.Duration
	timers map[string]*time.Timer
	fire   func(string)
	closed bool
}

func (d *debouncer) touch(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.fire == nil {
		return
	}
	if d.timers == nil {
		d.timers = map[string]*time.Timer{}
	}
	if t, ok := d.timers[key]; ok {
		t.Reset(d.wait)
		return
	}
	d.timers[key] = time.AfterFunc(d.wait, func() {
		d.mu.Lock()
		delete(d.timers, key)
		closed := d.closed
		d.mu.Unlock()
		if !closed {
			d.fire(key)
		}
	})
}

func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	for key, t := range d.timers {
		t.Stop()
		delete(d.timers, key)
	}
}
