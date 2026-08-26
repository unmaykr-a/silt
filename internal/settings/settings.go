// Package settings layers database overrides on top of the environment
// configuration.
//
// Silt is configured by environment variables, which is what a self-hoster
// expects and what a compose file can express. But an environment variable
// costs a container recreate to change, and some knobs — how long to keep
// things, where to send notifications, which environment keys stay readable —
// are ones you want to turn while looking at the screen that shows their
// effect.
//
// So: the environment is the baseline, the database holds a sparse set of
// overrides on top of it, and the effective configuration is the two merged
// and re-validated. Anything not in Editable is baseline-only, because it
// either cannot change without a restart (the listen address, the database
// path) or is a security boundary that must not be reachable through the very
// UI it protects (authentication, the compose-root allowlist).
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
)

// storeKey is the row in the settings table holding the override document.
// One JSON row rather than a column per knob: the set of editable knobs grows
// with the product, and a schema migration per knob would be pure ceremony.
const storeKey = "config_overrides"

// Storer is the persistence Live needs, kept narrow so the settings package
// does not depend on the whole store.
type Storer interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string) error
}

// Overrides is the sparse patch stored in the database. A nil field means
// "defer to the environment", which is what makes clearing an override
// distinguishable from setting it to a zero value.
type Overrides struct {
	SnapshotIntervalMS     *int64    `json:"snapshot_interval_ms,omitempty"`
	RetentionDays          *int      `json:"retention_days,omitempty"`
	UnchangedRetentionDays *int      `json:"unchanged_retention_days,omitempty"`
	EventRetentionDays     *int      `json:"event_retention_days,omitempty"`
	RetentionIntervalMS    *int64    `json:"retention_interval_ms,omitempty"`
	VacuumIntervalMS       *int64    `json:"vacuum_interval_ms,omitempty"`
	KeepKeys               *[]string `json:"keep_keys,omitempty"`
	BaseURL                *string   `json:"base_url,omitempty"`
	LogLevel               *string   `json:"log_level,omitempty"`
	NotifyURLs             *[]string `json:"notify_urls,omitempty"`
	NotifyOn               *[]string `json:"notify_on,omitempty"`
	NotifyMinSeverity      *string   `json:"notify_min_severity,omitempty"`
	IngestToken            *string   `json:"ingest_token,omitempty"`
}

// Fields names every override, in the order the settings screen shows them.
// Handlers use it to decide whether a patch key is known before touching the
// database, so a typo is a 400 rather than a silently ignored field.
var Fields = []string{
	"snapshot_interval_ms",
	"retention_days",
	"unchanged_retention_days",
	"event_retention_days",
	"retention_interval_ms",
	"vacuum_interval_ms",
	"keep_keys",
	"base_url",
	"log_level",
	"notify_urls",
	"notify_on",
	"notify_min_severity",
	"ingest_token",
}

// apply merges o onto base and returns the effective configuration.
func (o Overrides) apply(base config.Config) config.Config {
	c := base.Clone()
	if o.SnapshotIntervalMS != nil {
		c.SnapshotInterval = time.Duration(*o.SnapshotIntervalMS) * time.Millisecond
	}
	if o.RetentionDays != nil {
		c.RetentionDays = *o.RetentionDays
	}
	if o.UnchangedRetentionDays != nil {
		c.UnchangedRetentionDays = *o.UnchangedRetentionDays
	}
	if o.EventRetentionDays != nil {
		c.EventRetentionDays = *o.EventRetentionDays
	}
	if o.RetentionIntervalMS != nil {
		c.RetentionInterval = time.Duration(*o.RetentionIntervalMS) * time.Millisecond
	}
	if o.VacuumIntervalMS != nil {
		c.VacuumInterval = time.Duration(*o.VacuumIntervalMS) * time.Millisecond
	}
	if o.KeepKeys != nil {
		c.KeepKeys = clean(*o.KeepKeys)
	}
	if o.BaseURL != nil {
		c.BaseURL = strings.TrimSpace(*o.BaseURL)
	}
	if o.LogLevel != nil {
		c.LogLevel = *o.LogLevel
	}
	if o.NotifyURLs != nil {
		c.NotifyURLs = clean(*o.NotifyURLs)
	}
	if o.NotifyOn != nil {
		c.NotifyOn = clean(*o.NotifyOn)
	}
	if o.NotifyMinSeverity != nil {
		c.NotifyMinSeverity = *o.NotifyMinSeverity
	}
	if o.IngestToken != nil {
		c.IngestToken = *o.IngestToken
	}
	return c
}

// Set reports which fields carry an override, so the screen can show "set
// here" beside a value that no longer comes from the environment.
func (o Overrides) Set() map[string]bool {
	set := map[string]bool{}
	mark := func(name string, overridden bool) {
		if overridden {
			set[name] = true
		}
	}
	mark("snapshot_interval_ms", o.SnapshotIntervalMS != nil)
	mark("retention_days", o.RetentionDays != nil)
	mark("unchanged_retention_days", o.UnchangedRetentionDays != nil)
	mark("event_retention_days", o.EventRetentionDays != nil)
	mark("retention_interval_ms", o.RetentionIntervalMS != nil)
	mark("vacuum_interval_ms", o.VacuumIntervalMS != nil)
	mark("keep_keys", o.KeepKeys != nil)
	mark("base_url", o.BaseURL != nil)
	mark("log_level", o.LogLevel != nil)
	mark("notify_urls", o.NotifyURLs != nil)
	mark("notify_on", o.NotifyOn != nil)
	mark("notify_min_severity", o.NotifyMinSeverity != nil)
	mark("ingest_token", o.IngestToken != nil)
	return set
}

func clean(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Live holds the effective configuration and hands it to everything that reads
// configuration at runtime.
//
// Readers call Get on every use rather than caching a copy, which is what
// makes a change take effect without a restart, and — because Get returns a
// deep copy under a read lock — what keeps the race detector quiet.
type Live struct {
	mu        sync.RWMutex
	base      config.Config
	effective config.Config
	overrides Overrides

	db        Storer
	observers []func(config.Config)
}

// Load reads the stored overrides and returns the merged configuration.
//
// A stored document that no longer validates — a knob whose bounds tightened
// in a later version, say — is reported rather than silently dropped, but the
// baseline still comes up, because refusing to start over a bad retention
// value would lock someone out of the screen that fixes it.
func Load(ctx context.Context, base config.Config, db Storer) (*Live, error) {
	l := &Live{base: base.Clone(), effective: base.Clone(), db: db}
	if db == nil {
		return l, nil
	}
	raw, err := db.GetSetting(ctx, storeKey)
	if err != nil {
		// No row is the normal case on a fresh install.
		return l, nil //nolint:nilerr // absence is not a failure
	}
	var o Overrides
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return l, fmt.Errorf("stored settings are not readable, using the environment: %w", err)
	}
	merged := o.apply(base)
	if err := merged.Validate(); err != nil {
		return l, fmt.Errorf("stored settings are no longer valid, using the environment: %w", err)
	}
	l.overrides = o
	l.effective = merged
	return l, nil
}

// Get returns the effective configuration.
func (l *Live) Get() config.Config {
	if l == nil {
		return config.Config{}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.effective.Clone()
}

// Base returns the environment baseline, which the settings screen shows
// beside any value that has been overridden.
func (l *Live) Base() config.Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.base.Clone()
}

// Overrides returns the stored patch.
func (l *Live) Overrides() Overrides {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.overrides
}

// Observe registers a callback run after every successful change, and once
// immediately so a consumer does not need separate wiring for the initial
// value. Callbacks run outside the lock.
func (l *Live) Observe(fn func(config.Config)) {
	l.mu.Lock()
	l.observers = append(l.observers, fn)
	current := l.effective.Clone()
	l.mu.Unlock()
	fn(current)
}

// ErrReadOnly is returned when there is nowhere to persist a change.
var ErrReadOnly = errors.New("settings are read-only in this configuration")

// Update merges a patch into the stored overrides and, if the result
// validates, makes it effective.
//
// The whole merged document is validated before anything is written, so a
// rejected edit leaves both the database and the running configuration exactly
// as they were.
//
// Fields named in clear drop their override and fall back to the environment,
// which is how a value set from the UI is taken back rather than pinned to
// whatever it happened to be.
func (l *Live) Update(ctx context.Context, patch Overrides, clear []string) (config.Config, error) {
	if l.db == nil {
		return l.Get(), ErrReadOnly
	}
	for _, name := range clear {
		if !slices.Contains(Fields, name) {
			return config.Config{}, fmt.Errorf("unknown setting %q", name)
		}
	}

	l.mu.Lock()
	merged := clearFields(merge(l.overrides, patch), clear)
	next := merged.apply(l.base)
	if err := next.Validate(); err != nil {
		l.mu.Unlock()
		return config.Config{}, err
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		l.mu.Unlock()
		return config.Config{}, fmt.Errorf("encode settings: %w", err)
	}
	l.mu.Unlock()

	// Written before it is made effective: a value that is running but did not
	// survive the write would silently revert on the next restart.
	if err := l.db.PutSetting(ctx, storeKey, string(encoded)); err != nil {
		return config.Config{}, fmt.Errorf("save settings: %w", err)
	}

	l.mu.Lock()
	l.overrides = merged
	l.effective = next
	observers := slices.Clone(l.observers)
	current := next.Clone()
	l.mu.Unlock()

	for _, fn := range observers {
		fn(current.Clone())
	}
	return current, nil
}

// Reset drops every override, returning the install to its environment.
func (l *Live) Reset(ctx context.Context) (config.Config, error) {
	if l.db == nil {
		return l.Get(), ErrReadOnly
	}
	if err := l.db.PutSetting(ctx, storeKey, "{}"); err != nil {
		return config.Config{}, fmt.Errorf("save settings: %w", err)
	}

	l.mu.Lock()
	l.overrides = Overrides{}
	l.effective = l.base.Clone()
	observers := slices.Clone(l.observers)
	current := l.effective.Clone()
	l.mu.Unlock()

	for _, fn := range observers {
		fn(current.Clone())
	}
	return current, nil
}

// merge lays patch over current. Only fields present in patch move.
func merge(current, patch Overrides) Overrides {
	out := current
	if patch.SnapshotIntervalMS != nil {
		out.SnapshotIntervalMS = patch.SnapshotIntervalMS
	}
	if patch.RetentionDays != nil {
		out.RetentionDays = patch.RetentionDays
	}
	if patch.UnchangedRetentionDays != nil {
		out.UnchangedRetentionDays = patch.UnchangedRetentionDays
	}
	if patch.EventRetentionDays != nil {
		out.EventRetentionDays = patch.EventRetentionDays
	}
	if patch.RetentionIntervalMS != nil {
		out.RetentionIntervalMS = patch.RetentionIntervalMS
	}
	if patch.VacuumIntervalMS != nil {
		out.VacuumIntervalMS = patch.VacuumIntervalMS
	}
	if patch.KeepKeys != nil {
		out.KeepKeys = patch.KeepKeys
	}
	if patch.BaseURL != nil {
		out.BaseURL = patch.BaseURL
	}
	if patch.LogLevel != nil {
		out.LogLevel = patch.LogLevel
	}
	if patch.NotifyURLs != nil {
		out.NotifyURLs = patch.NotifyURLs
	}
	if patch.NotifyOn != nil {
		out.NotifyOn = patch.NotifyOn
	}
	if patch.NotifyMinSeverity != nil {
		out.NotifyMinSeverity = patch.NotifyMinSeverity
	}
	if patch.IngestToken != nil {
		out.IngestToken = patch.IngestToken
	}
	return out
}

// clearFields drops the named overrides.
func clearFields(o Overrides, names []string) Overrides {
	for _, name := range names {
		switch name {
		case "snapshot_interval_ms":
			o.SnapshotIntervalMS = nil
		case "retention_days":
			o.RetentionDays = nil
		case "unchanged_retention_days":
			o.UnchangedRetentionDays = nil
		case "event_retention_days":
			o.EventRetentionDays = nil
		case "retention_interval_ms":
			o.RetentionIntervalMS = nil
		case "vacuum_interval_ms":
			o.VacuumIntervalMS = nil
		case "keep_keys":
			o.KeepKeys = nil
		case "base_url":
			o.BaseURL = nil
		case "log_level":
			o.LogLevel = nil
		case "notify_urls":
			o.NotifyURLs = nil
		case "notify_on":
			o.NotifyOn = nil
		case "notify_min_severity":
			o.NotifyMinSeverity = nil
		case "ingest_token":
			o.IngestToken = nil
		}
	}
	return o
}
