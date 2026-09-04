package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Backing up a live database.
//
// Silt's whole value is history that cannot be reconstructed, and until now
// the only answer to "how do I back this up" was to copy silt.db — which is
// wrong in a way that does not announce itself. The database runs in WAL mode,
// so at any moment the committed state is spread across silt.db, silt.db-wal
// and silt.db-shm. Copying the first of those while Silt is running gets a
// file that opens, reports no error, and is missing whatever had not been
// checkpointed. The failure surfaces the day you restore it.
//
// VACUUM INTO is SQLite's answer: it runs in a read transaction, so it sees
// one consistent snapshot including everything in the WAL, and writes a
// compacted single file with no sidecars. The result is a database, not an
// archive format — you restore it by putting it where silt.db goes.

// BackupTo writes a consistent snapshot of the database to path.
//
// The destination must not exist; SQLite refuses to overwrite, and so does
// this, one step earlier and with a better message.
func (s *Store) BackupTo(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("backup path is empty")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup destination %s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	// On the read pool: VACUUM INTO only reads the source, and putting it on
	// the single writer would block every snapshot for the length of the copy.
	//
	// The path is a literal rather than a parameter because SQLite does not
	// accept a bound parameter here. It never comes from a request — the
	// handler builds it from a temporary directory it made itself — but it is
	// quoted properly anyway, because the day someone passes a name through is
	// the day that matters.
	if _, err := s.read.ExecContext(ctx, "VACUUM INTO "+quoteSQLiteString(path)); err != nil {
		return fmt.Errorf("vacuum into %s: %w", path, err)
	}
	return nil
}

// quoteSQLiteString wraps a value in single quotes, doubling any it contains.
func quoteSQLiteString(v string) string {
	out := make([]byte, 0, len(v)+2)
	out = append(out, '\'')
	for i := 0; i < len(v); i++ {
		if v[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, v[i])
	}
	return string(append(out, '\''))
}
