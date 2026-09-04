package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/unmaykr-a/silt/internal/store"
)

// getBackup streams a consistent snapshot of the database.
//
// This is the answer to the question Silt could not previously answer. Its
// entire value is a history nobody can reconstruct, and the documented way to
// keep a copy of that was to copy silt.db — which, in WAL mode and while Silt
// is running, produces a file that opens cleanly and is quietly missing
// whatever had not been checkpointed. A backup that looks fine and is not is
// worse than no backup, because it is the one you stop worrying about.
//
// A download rather than a scheduled job writing somewhere: Silt does not know
// where your backups live, and a tool that writes files into paths it was
// given is a tool with a new class of bug. Point whatever already backs up
// your host at this URL, or press the button.
//
// Administrator only, and it is worth saying why given how much else is
// readable to a viewer: the file is every project, every captured compose
// file, the audit trail and the session table in one download. Redaction means
// the values are keyed digests rather than secrets — that is the point of it —
// but "can read the screens" and "may walk off with the database" are not the
// same permission.
func (s *Server) getBackup(w http.ResponseWriter, r *http.Request) {
	// Checked here rather than left to the write guard, which lets every read
	// method through — correctly, for every other endpoint. This is the one
	// read that is not a screen, and the guard would have waved it past.
	if id, ok := s.identify(r); s.gate.Enabled() && (!ok || !id.IsAdmin()) {
		writeError(w, http.StatusForbidden,
			"downloading the database needs an administrator")
		return
	}

	// Temporary directory rather than beside the database: the data volume is
	// the thing most likely to be short of space, and a half-written backup
	// filling it would take Silt down to make a copy of it.
	dir, err := os.MkdirTemp("", "silt-backup-")
	if err != nil {
		s.log.Error("backup: temp dir", "error", err)
		writeError(w, http.StatusInternalServerError, "could not prepare a backup")
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "silt.db")
	if err := s.store.BackupTo(r.Context(), path); err != nil {
		s.log.Error("backup", "error", err)
		writeError(w, http.StatusInternalServerError, "could not write a backup")
		return
	}

	file, err := os.Open(path)
	if err != nil {
		s.log.Error("backup: open", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the backup")
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		s.log.Error("backup: stat", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the backup")
		return
	}

	name := fmt.Sprintf("silt-%s-%s.db",
		filenameSafe(s.conf().HostName), time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Cache-Control", "no-store")

	// ServeContent rather than io.Copy: it handles a client that goes away
	// mid-download without logging it as a failure, and a backup of a busy
	// host is large enough that this happens.
	http.ServeContent(w, r, name, info.ModTime(), file)

	s.audit(r, store.AuditBackup, map[string]any{"bytes": info.Size()})
}
