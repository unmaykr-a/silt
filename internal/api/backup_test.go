package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/store"
)

// The backup is the answer to the one question Silt could not previously
// answer about its own data. What matters is that the download is a database
// someone can actually open — not an error page with a .db name on it, and not
// a copy of a file being written to.

func TestTheBackupIsAnOpenableDatabaseWithTheHistoryInIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	resp, raw := f.get(t, "/api/maintenance/backup")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backup = %d %.120s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.sqlite3" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.HasSuffix(strings.TrimSuffix(cd, `"`), ".db") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	// The SQLite header. A JSON error body would sail past every other check.
	if !strings.HasPrefix(string(raw), "SQLite format 3\x00") {
		t.Fatalf("the download is not a SQLite database: %.60q", raw)
	}

	// Open it and read the history back, which is the only assertion that
	// actually proves a backup is a backup.
	path := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("the backup will not open: %v", err)
	}
	defer func() { _ = restored.Close() }()

	projects, err := restored.RQ.ListProjects(ctx, 1)
	if err != nil {
		t.Fatalf("read projects from the backup: %v", err)
	}
	if len(projects) == 0 {
		t.Error("the backup opened but has no projects in it")
	}
}

func TestTheBackupCarriesWritesThatArePresumablyStillInTheWAL(t *testing.T) {
	// The bug that makes copying silt.db wrong: in WAL mode the committed
	// state lives across three files, so a copy of the first one is missing
	// whatever has not been checkpointed. A write made immediately before the
	// backup is the case that catches it.
	f := newFixture(t)
	ctx := context.Background()

	if err := f.store.RecordAudit(ctx, store.AuditRecord{
		Actor: "someone", Action: store.AuditSettingsChanged, Method: store.AuditByLocal,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	resp, raw := f.get(t, "/api/maintenance/backup")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backup = %d", resp.StatusCode)
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = restored.Close() }()

	n, err := restored.CountAudit(ctx)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n == 0 {
		t.Error("a row written just before the backup is not in it")
	}
}

func TestTheBackupDoesNotOverwriteAnExistingFile(t *testing.T) {
	// The store refuses rather than letting SQLite decide, one step earlier
	// and with a message that says which file.
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "taken.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.store.BackupTo(context.Background(), path)
	if err == nil {
		t.Fatal("backing up over an existing file succeeded")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want it to say the destination exists", err)
	}
	if body, _ := os.ReadFile(path); string(body) != "not a database" {
		t.Error("the existing file was overwritten anyway")
	}
}
