package store_test

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

func timeNow() time.Time { return time.Now() }

// allBlobHashes reads hashes directly, so the scan does not depend on any
// query helper that could itself be filtering.
func allBlobHashes(ctx context.Context, path string) ([]string, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(ctx, "SELECT hash FROM blobs")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// checkpoint folds the WAL into the main database so size measurements are
// not at the mercy of checkpoint timing.
func checkpoint(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
