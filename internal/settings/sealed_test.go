package settings_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/secret"
	"github.com/unmaykr-a/silt/internal/settings"
	"github.com/unmaykr-a/silt/internal/store"
)

// planted is a secret-shaped value. With a key configured it must appear nowhere
// in the database file, its write-ahead log, or a backup taken through the API.
const planted = "SILT-SEALED-SENTINEL-6f1ed002ab5595859014ebf0951522d9"

// TestASealedSecretIsNotInTheDatabaseOrABackup is the whole reason
// SILT_SECRET_KEY exists, checked the way internal/store checks redaction: by
// byte-scanning what was written rather than by querying it.
//
// Querying would prove the value is not in the column it was meant to be in.
// Scanning proves it is not in the file — which is what leaves the host when
// someone downloads a backup, and the only claim worth making.
func TestASealedSecretIsNotInTheDatabaseOrABackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "silt.db")

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = db.Close() }()

	cipher, err := secret.New("a key kept in the environment")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	live, err := settings.Load(ctx, baseline(), db, cipher)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Every secret-tagged setting at once, so a new one added later is covered
	// by this test rather than needing another.
	values := map[string]any{}
	for _, f := range config.Editable() {
		if !f.Secret {
			continue
		}
		switch f.Name {
		case "notify_urls":
			values[f.Name] = []string{"generic://" + planted + "@example.invalid"}
		default:
			values[f.Name] = planted
		}
	}
	if len(values) == 0 {
		t.Fatal("no secret-tagged settings, so this test proves nothing")
	}
	patch, err := settings.Patch(values)
	if err != nil {
		t.Fatalf("build patch: %v", err)
	}
	if _, err := live.Update(ctx, patch, nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	// In force, in this process, as the plaintext it was given.
	if got := live.Get().IngestToken; got != planted {
		t.Fatalf("the running ingest token is %q, not the value that was saved", got)
	}

	// A checkpoint so the row is in the database file rather than only the WAL,
	// and a backup so the scan covers what someone actually downloads.
	backup := filepath.Join(dir, "backup.db")
	if err := db.BackupTo(ctx, backup); err != nil {
		t.Fatalf("backup: %v", err)
	}

	for _, path := range []string{dbPath, dbPath + "-wal", backup} {
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue // no WAL at this moment is fine
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("%s contains the plaintext secret", filepath.Base(path))
		}
	}

	// And it comes back on reload, because a secret that is safe but unreadable
	// is a broken install rather than a protected one.
	reloaded, err := settings.Load(ctx, baseline(), db, cipher)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Get().IngestToken; got != planted {
		t.Errorf("after reload the ingest token is %q, want the saved value", got)
	}
}

// Without a key the value is stored as it always was. Stated as a test rather
// than left implicit: it is the upgrade path, and it is also the exact claim the
// manual makes about what SILT_SECRET_KEY buys you.
func TestWithoutAKeyTheSecretIsStoredAsItAlwaysWas(t *testing.T) {
	ctx := context.Background()
	db := newMem()
	live, err := settings.Load(ctx, baseline(), db, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := live.Update(ctx, patch(t, map[string]any{"ingest_token": planted}), nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(db.rows["config_overrides"], planted) {
		t.Error("without a key the value was transformed anyway; that is not the documented behaviour")
	}
}

// A key added to an install that already has settings has to read them.
func TestAKeyAddedLaterReadsWhatIsAlreadyThere(t *testing.T) {
	ctx := context.Background()
	db := newMem()

	before, err := settings.Load(ctx, baseline(), db, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := before.Update(ctx, patch(t, map[string]any{"ingest_token": planted}), nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	cipher, _ := secret.New("added afterwards")
	after, err := settings.Load(ctx, baseline(), db, cipher)
	if err != nil {
		t.Fatalf("reload with a key: %v", err)
	}
	if got := after.Get().IngestToken; got != planted {
		t.Errorf("token = %q, want the plaintext value stored before the key existed", got)
	}

	// And the next save seals it, so adding a key is what migrates the value.
	if _, err := after.Update(ctx, patch(t, map[string]any{"base_url": "https://silt.example"}), nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if strings.Contains(db.rows["config_overrides"], planted) {
		t.Error("a save after the key was added left the secret in plaintext")
	}
}

// Removing the key must report rather than come up with a broken secret. The
// baseline still boots, because refusing to start would lock someone out of the
// screen that fixes it.
func TestRemovingTheKeyIsReportedAndFallsBackToTheEnvironment(t *testing.T) {
	ctx := context.Background()
	db := newMem()

	cipher, _ := secret.New("k")
	sealed, err := settings.Load(ctx, baseline(), db, cipher)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := sealed.Update(ctx, patch(t, map[string]any{"ingest_token": planted}), nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	live, err := settings.Load(ctx, baseline(), db, nil)
	if err == nil {
		t.Fatal("loading a sealed document with no key was silent")
	}
	if !strings.Contains(err.Error(), "SILT_SECRET_KEY") {
		t.Errorf("the error does not name the variable to restore: %v", err)
	}
	if live == nil {
		t.Fatal("no configuration at all came up; the settings screen would be unreachable")
	}
	if got := live.Get().RetentionDays; got != baseline().RetentionDays {
		t.Errorf("retention = %d, want the environment baseline", got)
	}
}
