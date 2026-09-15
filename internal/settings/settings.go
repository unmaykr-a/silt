// Package settings layers database overrides on top of the environment
// configuration.
//
// The environment is the baseline an install boots with, which is what a
// compose file can express and what a self-hoster expects to find there. But
// an environment variable costs a container recreate to change, and almost
// nothing here is worth a restart to turn. So the database holds a sparse set
// of overrides on top of the baseline, and the effective configuration is the
// two merged and re-validated — every value can say where it came from, and
// "use the environment value again" is a button rather than a matter of
// remembering what the old number was.
//
// What is editable is decided by the `editable` tag on config.Config rather
// than by a list kept here. Anything untagged is baseline-only, because it is
// consumed once by something that cannot be rebuilt in place: where the
// process listens, which database file it opens, and the compose-root
// allowlist, whose paths have to match a read-only volume mount to mean
// anything.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/secret"
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

// Overrides is the sparse patch stored in the database: the settings that have
// been given a value here, and nothing else.
//
// Absence is the point. A document that named every setting would pin them all
// to whatever they happened to be at the time, so the next edit to the compose
// file would change nothing and nobody would be able to see why.
//
// The values stay as raw JSON between the wire and the config struct. Decoding
// them earlier would mean a type per setting, which is the parallel list this
// replaced.
type Overrides struct {
	values map[string]json.RawMessage
}

// NewOverrides builds a patch from wire values, rejecting any name that is not
// an editable setting.
//
// A typo used to be accepted and ignored: the patch decoded into a struct, and
// encoding/json drops what it does not recognise, so PUT with retention_dayz
// answered 200 and changed nothing.
func NewOverrides(values map[string]json.RawMessage) (Overrides, error) {
	out := Overrides{values: make(map[string]json.RawMessage, len(values))}
	for name, raw := range values {
		if _, ok := config.EditableField(name); !ok {
			return Overrides{}, fmt.Errorf("unknown setting %q", name)
		}
		out.values[name] = raw
	}
	return out, nil
}

// Patch builds an override document from Go values rather than JSON, for
// callers that have the values in hand.
//
// Wire names and wire types: a duration is its whole milliseconds, because that
// is what the name says and what the API carries.
func Patch(values map[string]any) (Overrides, error) {
	raw := make(map[string]json.RawMessage, len(values))
	for name, v := range values {
		encoded, err := json.Marshal(v)
		if err != nil {
			return Overrides{}, fmt.Errorf("setting %q: %w", name, err)
		}
		raw[name] = encoded
	}
	return NewOverrides(raw)
}

// MarshalJSON writes the patch as a flat object of setting names to values,
// which is both the stored form and the exported one.
func (o Overrides) MarshalJSON() ([]byte, error) {
	if o.values == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(o.values)
}

// UnmarshalJSON reads that form back, rejecting unknown names.
func (o *Overrides) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := NewOverrides(raw)
	if err != nil {
		return err
	}
	*o = parsed
	return nil
}

// Has reports whether a setting carries an override.
func (o Overrides) Has(name string) bool {
	_, ok := o.values[name]
	return ok
}

// Len is how many settings are set here rather than by the environment.
func (o Overrides) Len() int { return len(o.values) }

// Names lists the overridden settings in the order the settings screen shows
// them, so a log line and the screen agree about the order.
func (o Overrides) Names() []string {
	out := make([]string, 0, len(o.values))
	for _, name := range config.EditableNames() {
		if o.Has(name) {
			out = append(out, name)
		}
	}
	return out
}

// Set reports which fields carry an override, so the screen can show "set
// here" beside a value that no longer comes from the environment.
func (o Overrides) Set() map[string]bool {
	set := make(map[string]bool, len(o.values))
	for name := range o.values {
		set[name] = true
	}
	return set
}

// WithoutSecrets drops the write-only values and names what it dropped.
//
// Used by the export, which is a file someone keeps: a shoutrrr URL carries
// the credential for the service it points at, and neither it nor the ingest
// token becomes readable by being called a backup. Driven by the tag rather
// than a hand-written pair, so a secret added later is excluded by default
// instead of leaking until someone remembers this function exists.
func (o Overrides) WithoutSecrets() (Overrides, []string) {
	out := Overrides{values: maps.Clone(o.values)}
	var dropped []string
	for _, f := range config.Editable() {
		if f.Secret && o.Has(f.Name) {
			delete(out.values, f.Name)
			dropped = append(dropped, f.Name)
		}
	}
	return out, dropped
}

// seal encrypts every secret-tagged value, and is applied on the way to the
// database rather than in memory: the running configuration needs the real
// values, and Apply decoding a ciphertext into a field would mean Silt
// presenting "enc:v1:…" to an identity provider.
//
// The whole JSON value is sealed and stored as a JSON string, whatever its type,
// so a list of notification targets is one ciphertext rather than a list of them.
// That keeps the shape uniform and stops the number of targets leaking through
// the length of the stored array.
func (o Overrides) seal(c *secret.Cipher) (Overrides, error) {
	if !c.Enabled() {
		return o, nil
	}
	out := Overrides{values: maps.Clone(o.values)}
	for _, f := range config.Editable() {
		if !f.Secret || !o.Has(f.Name) {
			continue
		}
		sealed, err := c.Seal(string(o.values[f.Name]))
		if err != nil {
			return Overrides{}, fmt.Errorf("setting %q: %w", f.Name, err)
		}
		encoded, err := json.Marshal(sealed)
		if err != nil {
			return Overrides{}, fmt.Errorf("setting %q: %w", f.Name, err)
		}
		out.values[f.Name] = encoded
	}
	return out, nil
}

// open reverses seal. A value that was stored before a key was configured has no
// prefix and comes back as it is, which is what lets a key be added to an
// install that already has settings saved.
func (o Overrides) open(c *secret.Cipher) (Overrides, error) {
	out := Overrides{values: maps.Clone(o.values)}
	for _, f := range config.Editable() {
		if !f.Secret || !o.Has(f.Name) {
			continue
		}
		// Only a JSON string can be a sealed value; anything else was stored
		// unsealed and is already the value.
		var sealed string
		if err := json.Unmarshal(o.values[f.Name], &sealed); err != nil {
			continue
		}
		if !strings.HasPrefix(sealed, secret.Prefix) {
			continue
		}
		plain, err := c.Open(sealed)
		if err != nil {
			return Overrides{}, fmt.Errorf("setting %q: %w", f.Name, err)
		}
		out.values[f.Name] = json.RawMessage(plain)
	}
	return out, nil
}

// Apply merges the patch onto base and returns the effective configuration,
// unvalidated. A value that will not decode into its field is an error here
// rather than a zero value there.
func (o Overrides) Apply(base config.Config) (config.Config, error) {
	c := base.Clone()
	// In field order rather than map order: two settings can both be wrong,
	// and the message should name the same one every time.
	for _, f := range config.Editable() {
		raw, ok := o.values[f.Name]
		if !ok {
			continue
		}
		if err := f.Decode(&c, raw); err != nil {
			return config.Config{}, err
		}
	}
	return c, nil
}

// merge lays patch over o. Only settings present in patch move.
func (o Overrides) merge(patch Overrides) Overrides {
	out := Overrides{values: maps.Clone(o.values)}
	if out.values == nil {
		out.values = map[string]json.RawMessage{}
	}
	maps.Copy(out.values, patch.values)
	return out
}

// without drops the named overrides, returning those settings to the
// environment.
func (o Overrides) without(names []string) Overrides {
	out := Overrides{values: maps.Clone(o.values)}
	for _, name := range names {
		delete(out.values, name)
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
	cipher    *secret.Cipher
	observers []func(config.Config)
}

// Load reads the stored overrides and returns the merged configuration.
//
// A stored document that no longer validates — a knob whose bounds tightened
// in a later version, say — is reported rather than silently dropped, but the
// baseline still comes up, because refusing to start over a bad retention
// value would lock someone out of the screen that fixes it.
func Load(ctx context.Context, base config.Config, db Storer, cipher *secret.Cipher) (*Live, error) {
	if cipher == nil {
		cipher = &secret.Cipher{}
	}
	l := &Live{base: base.Clone(), effective: base.Clone(), db: db, cipher: cipher}
	if db == nil {
		return l, nil
	}
	// The way back in from a save that locked someone out. Checked before the
	// stored document is read at all, so a document that will not even decode
	// is still recoverable.
	if base.SettingsReset {
		if err := db.PutSetting(ctx, storeKey, "{}"); err != nil {
			return l, fmt.Errorf("SILT_SETTINGS_RESET is set but the overrides could not be dropped: %w", err)
		}
		return l, nil
	}
	raw, err := db.GetSetting(ctx, storeKey)
	if err != nil {
		// No row is the normal case on a fresh install.
		return l, nil //nolint:nilerr // absence is not a failure
	}
	var stored Overrides
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return l, fmt.Errorf("stored settings are not readable, using the environment: %w", err)
	}
	o, err := stored.open(cipher)
	if err != nil {
		return l, fmt.Errorf("stored settings are not readable, using the environment: %w", err)
	}
	merged, err := o.Apply(base)
	if err != nil {
		return l, fmt.Errorf("stored settings are not readable, using the environment: %w", err)
	}
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

// Preview reports what a patch would produce without storing it, so a handler
// can reject a value on a rule this package has no business knowing — what
// counts as a change kind, say — before anything is written.
func (l *Live) Preview(patch Overrides, clear []string) (config.Config, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.overrides.merge(patch).without(clear).Apply(l.base)
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
		if _, ok := config.EditableField(name); !ok {
			return config.Config{}, fmt.Errorf("unknown setting %q", name)
		}
	}

	l.mu.Lock()
	merged := l.overrides.merge(patch).without(clear)
	next, err := merged.Apply(l.base)
	if err != nil {
		l.mu.Unlock()
		return config.Config{}, err
	}
	if err := next.Validate(); err != nil {
		l.mu.Unlock()
		return config.Config{}, err
	}
	sealed, err := merged.seal(l.cipher)
	if err != nil {
		l.mu.Unlock()
		return config.Config{}, err
	}
	encoded, err := json.Marshal(sealed)
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
