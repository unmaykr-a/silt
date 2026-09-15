package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// A setting is editable at runtime because it says so on the struct.
//
// The alternative, and what this replaced, was a parallel list: a field on an
// Overrides struct, an entry in a Fields slice, and a case in each of apply,
// Set, merge and clearFields. Six places per setting, five of them pure
// switch-case, and forgetting the merge case produced a setting that saved and
// then reverted on the next save of anything else — silently, because nothing
// compared the six lists to each other.
//
// Now the struct tag is the list. `editable:"retention_days"` names the JSON
// key the API and the settings screen use; adding `,secret` marks a value that
// is written but never read back.
const (
	editableTag = "editable"
	secretOpt   = "secret"
	gateOpt     = "gate"
)

// Field is one editable setting: where it lives on Config, what it is called
// on the wire, and how to move a JSON value in and out of it.
type Field struct {
	// Name is the JSON key, and the name the settings screen renders it under.
	Name string
	// Env is the environment variable that provides the baseline.
	Env string
	// Secret values travel inwards only. They are never returned by the API
	// and never included in an export, because a shoutrrr URL is the
	// credential for the service it points at and the ingest token is a
	// credential outright.
	Secret bool
	// Gate marks a setting the authentication gate is built from, so a change
	// to it has to rebuild that gate.
	//
	// Needed because a save hands the whole configuration to every observer,
	// and rebuilding means re-reading the account, re-reading the proxy rules
	// and re-running OpenID Connect discovery — which reaches the network.
	// Without this, changing the log level would make Silt call your identity
	// provider.
	Gate bool

	index int
	kind  kind
}

// kind is the shape of a field, which decides how JSON maps onto it.
type kind int

const (
	kindString kind = iota
	kindInt
	kindInt64
	kindBool
	kindStrings
	// kindDurationMS is a time.Duration carried over the wire as whole
	// milliseconds. A JSON number is the only honest option — "5m" would mean
	// teaching the settings screen Go's duration grammar — and milliseconds
	// rather than seconds because that is what Duration.Milliseconds gives and
	// what the existing wire names already say.
	kindDurationMS
)

// Editable lists every editable setting, in the order the fields are declared
// — which is the order the settings screen shows them, so the two cannot
// disagree about what this install contains.
var Editable = sync.OnceValue(func() []Field {
	var out []Field
	t := reflect.TypeOf(Config{})
	for i := range t.NumField() {
		sf := t.Field(i)
		tag := sf.Tag.Get(editableTag)
		if tag == "" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		f := Field{
			Name:   name,
			Env:    sf.Tag.Get("env"),
			Secret: tagOpts(opts)[secretOpt],
			Gate:   tagOpts(opts)[gateOpt],
			index:  i,
		}
		// A bad tag is a programming error, and the only useful time to find
		// out is the first test that touches this package rather than the
		// first save that silently does nothing.
		k, err := kindOf(sf.Type)
		if err != nil {
			panic(fmt.Sprintf("config: field %s: %v", sf.Name, err))
		}
		f.kind = k
		if f.Name == "" {
			panic("config: field " + sf.Name + ": editable tag has no name")
		}
		// The unit is part of the name because the value is a bare number: a
		// key called retention_interval carrying 3600000 would be read as
		// seconds by the next person to look at it.
		if k == kindDurationMS && !strings.HasSuffix(f.Name, "_ms") {
			panic("config: field " + sf.Name + ": a duration's editable name must end in _ms, got " + f.Name)
		}
		out = append(out, f)
	}
	return out
})

// EditableNames is Editable reduced to the wire names, in the same order.
var EditableNames = sync.OnceValue(func() []string {
	fields := Editable()
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
})

// GateFingerprint summarises every setting the authentication gate is built
// from, so a caller can tell whether a save actually changed any of them.
//
// A fingerprint rather than a comparison of two Configs: most of the gate's
// settings are slices, so == will not compile, and a hand-written comparison of
// twenty-one fields is the parallel list this package exists to avoid.
func GateFingerprint(c Config) string {
	var b strings.Builder
	for _, f := range Editable() {
		if !f.Gate {
			continue
		}
		raw, err := f.Encode(c)
		if err != nil {
			// Unreachable for the types a setting can have, and a fingerprint
			// that cannot be computed must compare unequal rather than equal:
			// rebuilding the gate needlessly is recoverable, not rebuilding it
			// when authentication changed is not.
			return "unfingerprintable:" + f.Name + ":" + err.Error()
		}
		b.WriteString(f.Name)
		b.WriteByte('=')
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// EditableField finds a setting by its wire name.
func EditableField(name string) (Field, bool) {
	for _, f := range Editable() {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func kindOf(t reflect.Type) (kind, error) {
	if t == reflect.TypeOf(time.Duration(0)) {
		return kindDurationMS, nil
	}
	switch t.Kind() {
	case reflect.String:
		return kindString, nil
	case reflect.Int:
		return kindInt, nil
	case reflect.Int64:
		return kindInt64, nil
	case reflect.Bool:
		return kindBool, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return kindStrings, nil
		}
	}
	return 0, fmt.Errorf("type %s cannot be an editable setting", t)
}

// Decode writes a JSON value into this field of c.
//
// Values are normalised on the way in, the same way the environment loader
// normalises them: strings are trimmed, and a list drops the empties that a
// trailing comma in a text box leaves behind. Without that, a keep-key typed
// as "FOO, " would be stored as two entries, one of them blank.
func (f Field) Decode(c *Config, raw json.RawMessage) error {
	v := reflect.ValueOf(c).Elem().Field(f.index)
	fail := func(want string) error {
		return fmt.Errorf("%s must be %s", f.Name, want)
	}
	switch f.kind {
	case kindString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fail("a string")
		}
		v.SetString(strings.TrimSpace(s))
	case kindInt, kindInt64:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return fail("a whole number")
		}
		if v.OverflowInt(n) {
			return fail("a smaller number")
		}
		v.SetInt(n)
	case kindDurationMS:
		var ms int64
		if err := json.Unmarshal(raw, &ms); err != nil {
			return fail("a whole number of milliseconds")
		}
		v.SetInt(int64(time.Duration(ms) * time.Millisecond))
	case kindBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return fail("true or false")
		}
		v.SetBool(b)
	case kindStrings:
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return fail("a list of strings")
		}
		v.Set(reflect.ValueOf(clean(list)))
	}
	return nil
}

// Encode reads this field of c back out as JSON, which is what lets a test
// assert that what goes in comes out and what the ceiling check compares
// against.
func (f Field) Encode(c Config) (json.RawMessage, error) {
	v := reflect.ValueOf(c).Field(f.index)
	if f.kind == kindDurationMS {
		return json.Marshal(time.Duration(v.Int()).Milliseconds())
	}
	if f.kind == kindStrings && v.IsNil() {
		return json.Marshal([]string{})
	}
	return json.Marshal(v.Interface())
}

// clean trims a list and drops the blanks.
func clean(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tagOpts parses the comma-separated options half of a struct tag.
func tagOpts(opts string) map[string]bool {
	out := map[string]bool{}
	for _, o := range strings.Split(opts, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out[o] = true
		}
	}
	return out
}
