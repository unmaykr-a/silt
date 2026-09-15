package collect

import (
	"context"
	"log/slog"
	"sync"
)

// HostRenamer keeps this host's stored history attached to its current label.
//
// SILT_HOST_NAME is the key the host row is upserted on, so it is not the
// cosmetic setting it looks like: left alone, changing it makes the next
// snapshot insert a second host and re-create every project under it, leaving
// all the history under the old name and none under the new one. That was
// already true of editing the environment variable and restarting. Making the
// name a text box is what turns it from a footgun into a thing that has to
// work.
//
// It lives here, rather than inline in the observer that calls it, because the
// wiring is the part that can be wrong: whether the previous name is remembered
// correctly, and what happens when two administrators save at once. Neither is
// checkable in a function literal inside main.
type HostRenamer struct {
	Store HostStore
	Log   *slog.Logger

	mu      sync.Mutex
	applied string
}

// HostStore is the persistence a renamer needs.
type HostStore interface {
	RenameHost(ctx context.Context, from, to string) (bool, error)
}

// NewHostRenamer starts from the name the process booted with.
func NewHostRenamer(st HostStore, current string, log *slog.Logger) *HostRenamer {
	if log == nil {
		log = slog.Default()
	}
	return &HostRenamer{Store: st, Log: log, applied: current}
}

// Apply moves the history when the name has changed since the last call, and
// reports whether a rename was carried out.
//
// The comparison and the bookkeeping happen under the lock while the write does
// not, so two saves landing together produce one rename rather than two racing
// ones — and the second, seeing the name it was told to move to already
// applied, does nothing.
func (h *HostRenamer) Apply(ctx context.Context, name string) bool {
	if h == nil || name == "" {
		return false
	}
	h.mu.Lock()
	previous := h.applied
	if previous == name {
		h.mu.Unlock()
		return false
	}
	h.applied = name
	h.mu.Unlock()

	renamed, err := h.Store.RenameHost(ctx, previous, name)
	switch {
	case err != nil:
		h.Log.Error("host rename failed; the history stays under the old name",
			"from", previous, "to", name, "error", err)
		return false
	case renamed:
		h.Log.Info("host renamed", "from", previous, "to", name)
		return true
	default:
		// Renaming back to a label used before finds a row already holding it.
		// Two histories stay two: merging them would mean deciding which host's
		// snapshots win for a project both have seen, and there is no safe
		// default for that.
		h.Log.Warn("host name changed, but a host already holds that name; this host attaches to the existing one",
			"from", previous, "to", name)
		return false
	}
}
