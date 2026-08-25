// Package store persists Silt's observations in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go driver; cgo would break arm64 cross-compilation

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store owns the database connections.
//
// Two pools against one file, deliberately. SQLite takes a single writer, and
// a mutex around writes is a rule someone eventually forgets; capping the
// write pool at one connection makes it structural. Pragmas go in the DSN
// because they are per-connection — setting them once on one connection does
// not apply to the pool.
type Store struct {
	write *sql.DB
	read  *sql.DB

	// Q runs on the write pool.
	Q *sqlcgen.Queries
	// RQ runs on the read pool.
	RQ *sqlcgen.Queries
}

// Now returns the current time as unix milliseconds, the unit every timestamp
// in the schema uses.
func Now() int64 { return time.Now().UnixMilli() }

// dsn builds a connection string with the pragmas every connection needs.
func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	// NORMAL is the documented companion to WAL: durable across process
	// crashes, and it avoids an fsync per transaction on an SD card.
	q.Add("_pragma", "synchronous(NORMAL)")
	if readOnly {
		q.Add("mode", "ro")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open prepares the database at path, running migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory %q: %w", dir, err)
		}
	}

	write, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// The single writer, enforced structurally rather than by convention.
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)

	if err := write.PingContext(ctx); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := migrate(ctx, write); err != nil {
		_ = write.Close()
		return nil, err
	}

	read, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	read.SetMaxOpenConns(4)

	return &Store{
		write: write,
		read:  read,
		Q:     sqlcgen.New(write),
		RQ:    sqlcgen.New(read),
	}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// Close releases both pools.
func (s *Store) Close() error {
	return errors.Join(s.read.Close(), s.write.Close())
}

// Tx runs fn inside a write transaction, rolling back on error.
func (s *Store) Tx(ctx context.Context, fn func(*sqlcgen.Queries) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(sqlcgen.New(tx).WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// Vacuum reclaims free pages. It rewrites the whole file, so callers should
// run it rarely.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.write.ExecContext(ctx, "VACUUM")
	return err
}
