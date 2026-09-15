package store

import (
	"context"
	"fmt"
)

// RenameHost moves this host's history to a new label, reporting whether it
// did.
//
// SILT_HOST_NAME is the key the host row is upserted on, so changing it is not
// the cosmetic edit it looks like: without this, the next snapshot would insert
// a second host and every project would be re-created under it, leaving the old
// name holding all the history and the new one holding none. That was already
// true of changing the environment variable and restarting; making it a text
// box on the settings screen is what makes it worth fixing rather than
// documenting.
//
// A rename happens only when the old name exists and the new one does not.
// Renaming back to a label used before finds a row already holding it, and two
// histories are left as two: merging them would mean deciding which host's
// snapshots win for a project both have seen, which is a question with no safe
// default. The caller attaches to the existing row instead, which is what the
// upsert does anyway.
//
// Hand-written rather than generated for the reason PROJECT.md Section 15
// records about sqlc's SQLite grammar — and because the whole operation is one
// conditional UPDATE, which a typed wrapper would only wrap.
func (s *Store) RenameHost(ctx context.Context, from, to string) (bool, error) {
	if from == "" || to == "" || from == to {
		return false, nil
	}
	res, err := s.write.ExecContext(ctx, `
		UPDATE hosts SET name = ?
		WHERE name = ?
		  AND NOT EXISTS (SELECT 1 FROM hosts WHERE name = ?)`,
		to, from, to)
	if err != nil {
		return false, fmt.Errorf("rename host %q to %q: %w", from, to, err)
	}
	moved, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rename host: %w", err)
	}
	return moved > 0, nil
}
