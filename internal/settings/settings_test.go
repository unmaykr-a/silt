package settings_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/settings"
)

// memStore is the settings table, without the database.
type memStore struct {
	rows  map[string]string
	fails bool
}

func newMem() *memStore { return &memStore{rows: map[string]string{}} }

func (m *memStore) GetSetting(_ context.Context, key string) (string, error) {
	v, ok := m.rows[key]
	if !ok {
		return "", errors.New("no rows")
	}
	return v, nil
}

func (m *memStore) PutSetting(_ context.Context, key, value string) error {
	if m.fails {
		return errors.New("disk is full")
	}
	m.rows[key] = value
	return nil
}

func baseline() config.Config {
	return config.Config{
		ListenAddr:             ":8375",
		LogLevel:               "info",
		DockerHost:             "tcp://docker-socket-proxy:2375",
		DBPath:                 "/data/silt.db",
		SnapshotInterval:       5 * time.Minute,
		RetentionInterval:      time.Hour,
		RetentionDays:          365,
		UnchangedRetentionDays: 7,
		EventRetentionDays:     90,
		NotifyMinSeverity:      "medium",
		MaxComposeFileBytes:    1 << 20,
		SessionTTL:             720 * time.Hour,
		SessionIdleTTL:         168 * time.Hour,
		KeepKeys:               []string{"FROM_ENV"},
	}
}

func ptr[T any](v T) *T { return &v }

func TestOverrideTakesEffectAndSurvivesAReload(t *testing.T) {
	db := newMem()
	live, err := settings.Load(t.Context(), baseline(), db)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := live.Get().RetentionDays; got != 365 {
		t.Fatalf("baseline retention = %d, want 365", got)
	}

	if _, err := live.Update(t.Context(), settings.Overrides{RetentionDays: ptr(30)}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := live.Get().RetentionDays; got != 30 {
		t.Errorf("retention after update = %d, want 30", got)
	}
	if got := live.Base().RetentionDays; got != 365 {
		t.Errorf("baseline moved to %d; the environment must stay the baseline", got)
	}

	reloaded, err := settings.Load(t.Context(), baseline(), db)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Get().RetentionDays; got != 30 {
		t.Errorf("retention after reload = %d, want 30", got)
	}
}

// An edit that does not validate must not reach the database, or a restart
// would come up with a configuration the running process refused.
func TestRejectedUpdateChangesNothing(t *testing.T) {
	db := newMem()
	live, _ := settings.Load(t.Context(), baseline(), db)

	if _, err := live.Update(t.Context(), settings.Overrides{SnapshotIntervalMS: ptr(int64(10))}, nil); err == nil {
		t.Fatal("a 10ms snapshot interval was accepted")
	}
	if got := live.Get().SnapshotInterval; got != 5*time.Minute {
		t.Errorf("interval = %v, want the baseline 5m", got)
	}
	if _, stored := db.rows["config_overrides"]; stored {
		t.Error("a rejected edit was written to the database")
	}
}

// The cross-field rule is one of the reasons the whole merged document is
// validated rather than the patch: unchanged retention alone is fine, and only
// wrong beside the changed-retention value it now exceeds.
func TestCrossFieldValidationSeesTheMergedDocument(t *testing.T) {
	db := newMem()
	live, _ := settings.Load(t.Context(), baseline(), db)

	if _, err := live.Update(t.Context(), settings.Overrides{UnchangedRetentionDays: ptr(400)}, nil); err == nil {
		t.Fatal("unchanged retention was allowed to exceed changed retention")
	}
}

func TestResetFieldFallsBackToTheEnvironment(t *testing.T) {
	db := newMem()
	live, _ := settings.Load(t.Context(), baseline(), db)

	if _, err := live.Update(t.Context(), settings.Overrides{
		RetentionDays: ptr(30),
		BaseURL:       ptr("https://silt.example"),
	}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := live.Update(t.Context(), settings.Overrides{}, []string{"retention_days"}); err != nil {
		t.Fatalf("reset field: %v", err)
	}
	if got := live.Get().RetentionDays; got != 365 {
		t.Errorf("retention after reset = %d, want the environment's 365", got)
	}
	if got := live.Get().BaseURL; got != "https://silt.example" {
		t.Errorf("resetting one field cleared another: base_url = %q", got)
	}
	if !live.Overrides().Set()["base_url"] {
		t.Error("base_url should still be reported as overridden")
	}
	if live.Overrides().Set()["retention_days"] {
		t.Error("retention_days should no longer be reported as overridden")
	}
}

func TestResetDropsEverything(t *testing.T) {
	db := newMem()
	live, _ := settings.Load(t.Context(), baseline(), db)
	if _, err := live.Update(t.Context(), settings.Overrides{RetentionDays: ptr(30)}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := live.Reset(t.Context()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(live.Overrides().Set()) != 0 {
		t.Errorf("overrides survived a reset: %v", live.Overrides().Set())
	}
	if got := live.Get().RetentionDays; got != 365 {
		t.Errorf("retention after reset = %d, want 365", got)
	}
}

func TestUnknownResetFieldIsRejected(t *testing.T) {
	db := newMem()
	live, _ := settings.Load(t.Context(), baseline(), db)
	if _, err := live.Update(t.Context(), settings.Overrides{}, []string{"db_path"}); err == nil {
		t.Fatal("db_path is not editable but was accepted as a reset target")
	}
}

// A failed write must not leave the process running a value the database does
// not have.
func TestFailedWriteDoesNotChangeTheRunningConfiguration(t *testing.T) {
	db := newMem()
	db.fails = true
	live, _ := settings.Load(t.Context(), baseline(), db)

	if _, err := live.Update(t.Context(), settings.Overrides{RetentionDays: ptr(30)}, nil); err == nil {
		t.Fatal("update succeeded despite the write failing")
	}
	if got := live.Get().RetentionDays; got != 365 {
		t.Errorf("retention = %d, want the unchanged 365", got)
	}
}

// The baseline must come up even when the stored document is unusable: the
// screen that fixes a bad setting is served by the process that would
// otherwise refuse to start.
func TestUnreadableStoredSettingsFallBackToTheEnvironment(t *testing.T) {
	db := newMem()
	db.rows["config_overrides"] = "{not json"

	live, err := settings.Load(t.Context(), baseline(), db)
	if err == nil {
		t.Error("a corrupt document should be reported")
	}
	if got := live.Get().RetentionDays; got != 365 {
		t.Errorf("retention = %d, want the environment's 365", got)
	}
}

func TestObserversSeeTheCurrentValueImmediatelyAndOnChange(t *testing.T) {
	db := newMem()
	live, _ := settings.Load(t.Context(), baseline(), db)

	var seen []int
	live.Observe(func(c config.Config) { seen = append(seen, c.RetentionDays) })
	if len(seen) != 1 || seen[0] != 365 {
		t.Fatalf("observer saw %v on registration, want [365]", seen)
	}
	if _, err := live.Update(t.Context(), settings.Overrides{RetentionDays: ptr(30)}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(seen) != 2 || seen[1] != 30 {
		t.Errorf("observer saw %v, want the change appended", seen)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	live, _ := settings.Load(t.Context(), baseline(), newMem())
	got := live.Get()
	got.KeepKeys[0] = "MUTATED"
	if live.Get().KeepKeys[0] != "FROM_ENV" {
		t.Error("Get handed out the live slice; a caller can rewrite the keep-list")
	}
}

func TestReadOnlyWithoutAStore(t *testing.T) {
	live, err := settings.Load(t.Context(), baseline(), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := live.Update(t.Context(), settings.Overrides{RetentionDays: ptr(30)}, nil); !errors.Is(err, settings.ErrReadOnly) {
		t.Errorf("Update error = %v, want ErrReadOnly", err)
	}
}
