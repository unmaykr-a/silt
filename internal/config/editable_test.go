package config_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/config"
)

// TestEveryEditableSettingHasAnEnvironmentVariable keeps the two halves of a
// setting together. The environment is the baseline an override sits on top of,
// so a tagged field with no env tag would be a setting whose "use the
// environment value" button had nothing to fall back to.
func TestEveryEditableSettingHasAnEnvironmentVariable(t *testing.T) {
	for _, f := range config.Editable() {
		if f.Env == "" {
			t.Errorf("editable setting %q has no environment variable to fall back to", f.Name)
		}
		if !strings.HasPrefix(f.Env, "SILT_") {
			t.Errorf("editable setting %q reads %q, which is not one of Silt's variables", f.Name, f.Env)
		}
	}
}

// TestEditableNamesAreUniqueAndSnakeCase: the name is a JSON key, a search
// index entry and a column in the manual's settings table, so two fields
// sharing one would make a save write the wrong field.
func TestEditableNamesAreUniqueAndSnakeCase(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range config.Editable() {
		if seen[f.Name] {
			t.Errorf("two settings are both called %q", f.Name)
		}
		seen[f.Name] = true
		if f.Name != strings.ToLower(f.Name) || strings.ContainsAny(f.Name, " -.") {
			t.Errorf("setting name %q is not snake_case", f.Name)
		}
	}
}

// TestADurationSettingSaysItsUnit. The wire value is a bare number, so a key
// called retention_interval carrying 3600000 would be read as seconds by the
// next person to look at it. Editable panics on one that does not say _ms; this
// says the rule out loud, where someone adding a setting will read it.
func TestADurationSettingSaysItsUnit(t *testing.T) {
	durations := reflect.TypeOf(time.Duration(0))
	st := reflect.TypeOf(config.Config{})
	for i := range st.NumField() {
		sf := st.Field(i)
		name, _, _ := strings.Cut(sf.Tag.Get("editable"), ",")
		if name == "" || sf.Type != durations {
			continue
		}
		if !strings.HasSuffix(name, "_ms") {
			t.Errorf("%s is a duration called %q; the name must end in _ms because the value is a bare number",
				sf.Name, name)
		}
	}
}

// TestEverySettingSurvivesADecodeEncodeCycle covers the reflection in both
// directions at once, per kind. A field that decoded into the wrong place, or
// encoded a Duration as nanoseconds, comes back as a different value here.
func TestEverySettingSurvivesADecodeEncodeCycle(t *testing.T) {
	values := map[string]string{
		"log_level":                `"warn"`,
		"snapshot_interval_ms":     `90000`,
		"retention_days":           `7`,
		"unchanged_retention_days": `3`,
		"event_retention_days":     `21`,
		"audit_retention_days":     `42`,
		"vacuum_interval_ms":       `0`,
		"retention_interval_ms":    `120000`,
		"keep_keys":                `["TZ"]`,
		"ingest_token":             `"token"`,
		"notify_urls":              `["generic://x.invalid"]`,
		"notify_on":                `["image_id","volumes"]`,
		"notify_min_severity":      `"low"`,
		"base_url":                 `"https://silt.example"`,
		"docker_host":              `"tcp://other-proxy:2375"`,
		"host_name":                `"pi"`,
		"metrics_public":           `true`,
		"ingest_rate_per_minute":   `120`,
		"max_compose_file_bytes":   `2097152`,
		"trust_proxy_auth":         `true`,
		"auth_header":              `"X-Forwarded-User"`,
		"auth_groups_header":       `"X-Forwarded-Groups"`,
		"admin_groups":             `["silt-admins"]`,
		"trusted_proxies":          `["10.0.0.0/8"]`,
		"local_account":            `false`,
		"oidc_issuer":              `"https://auth.example.invalid/application/o/silt/"`,
		"oidc_client_id":           `"silt"`,
		"oidc_client_secret":       `"a-client-secret"`,
		"oidc_redirect_url":        `"https://silt.example/api/auth/callback"`,
		"oidc_scopes":              `["openid","profile"]`,
		"oidc_username_claim":      `"email"`,
		"oidc_groups_claim":        `"roles"`,
		"oidc_admin_groups":        `["silt-admins"]`,
		"oidc_allowed_groups":      `["silt-users"]`,
		"oidc_allowed_users":       `["someone@example.invalid"]`,
		"session_ttl_ms":           `604800000`,
		"session_idle_ttl_ms":      `86400000`,
		"oidc_admin_ttl_ms":        `3600000`,
		"cookie_secure":            `"always"`,
	}
	for _, f := range config.Editable() {
		raw, ok := values[f.Name]
		if !ok {
			t.Errorf("no decode/encode value for %q", f.Name)
			continue
		}
		var c config.Config
		if err := f.Decode(&c, json.RawMessage(raw)); err != nil {
			t.Errorf("%s: decode %s: %v", f.Name, raw, err)
			continue
		}
		out, err := f.Encode(c)
		if err != nil {
			t.Errorf("%s: encode: %v", f.Name, err)
			continue
		}
		var want, got any
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatalf("%s: test value is not JSON: %v", f.Name, err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: encoded to non-JSON %s: %v", f.Name, out, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: decoded %s and encoded back %s", f.Name, raw, out)
		}
	}
}

// TestADecodeOfTheWrongShapeIsRefused. The patch comes from HTTP, so a string
// where a number belongs is a request someone made, not a programming error —
// and it has to be a refusal rather than a zero value quietly replacing a
// retention window.
func TestADecodeOfTheWrongShapeIsRefused(t *testing.T) {
	for _, tc := range []struct{ setting, value string }{
		{"retention_days", `"thirty"`},
		{"snapshot_interval_ms", `"5m"`},
		{"keep_keys", `"TZ"`},
		{"base_url", `["https://silt.example"]`},
		{"log_level", `7`},
	} {
		f, ok := config.EditableField(tc.setting)
		if !ok {
			t.Fatalf("%s is not an editable setting", tc.setting)
		}
		var c config.Config
		if err := f.Decode(&c, json.RawMessage(tc.value)); err == nil {
			t.Errorf("%s accepted %s", tc.setting, tc.value)
		} else if !strings.Contains(err.Error(), tc.setting) {
			t.Errorf("%s: the refusal does not name the setting: %v", tc.setting, err)
		}
	}
}

// TestValuesAreNormalisedOnTheWayIn. A list typed into a text box arrives with
// the spaces and the trailing comma still in it; stored as-is, a keep-list of
// "TZ, " would be two entries, one of them blank, and the blank one would never
// match anything but would sit in the manual's idea of what is kept.
func TestValuesAreNormalisedOnTheWayIn(t *testing.T) {
	keep, _ := config.EditableField("keep_keys")
	var c config.Config
	if err := keep.Decode(&c, json.RawMessage(`[" TZ ","","PUID"]`)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := []string{"TZ", "PUID"}; !reflect.DeepEqual(c.KeepKeys, want) {
		t.Errorf("keep_keys = %q, want %q", c.KeepKeys, want)
	}

	base, _ := config.EditableField("base_url")
	if err := base.Decode(&c, json.RawMessage(`"  https://silt.example  "`)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.BaseURL != "https://silt.example" {
		t.Errorf("base_url = %q, want it trimmed", c.BaseURL)
	}
}

// TestThePackageDocExplainsEverySettingItWithholds.
//
// Not editable is the interesting claim, because it is the one that used to be
// policy: twenty-nine settings were described as deliberately environment-only
// when two of them were, one was an allowlist tied to a volume mount, and the
// rest were the price of a mechanism nobody wanted to pay again.
//
// So each one that stays has to say why, in the package doc, by name. This test
// exists because writing that list by hand got it wrong on the first attempt —
// it said five and named five, and there were six.
func TestThePackageDocExplainsEverySettingItWithholds(t *testing.T) {
	doc := read(t, "config.go")
	// Only the package comment: the struct's own field comments mention every
	// variable, so scanning the whole file would pass regardless.
	doc = doc[:strings.Index(doc, "package config")]

	editable := map[string]bool{}
	for _, f := range config.Editable() {
		editable[f.Env] = true
	}

	var withheld []string
	for _, env := range envTags(t) {
		if !editable[env] {
			withheld = append(withheld, env)
		}
	}
	if len(withheld) == 0 {
		t.Fatal("no settings are environment-only, so this test proves nothing")
	}
	for _, env := range withheld {
		if !strings.Contains(doc, env) {
			t.Errorf("%s is not editable and the package doc does not say why", env)
		}
	}

	// And the count in the prose, because "six settings have no such tag" is
	// the sentence someone reads instead of counting.
	counts := map[int]string{
		2: "Two", 3: "Three", 4: "Four", 5: "Five", 6: "Six", 7: "Seven",
		8: "Eight", 9: "Nine", 10: "Ten",
	}
	if word, ok := counts[len(withheld)]; ok && !strings.Contains(doc, word+" settings have no such tag") {
		t.Errorf("%d settings are environment-only; the package doc does not say %q", len(withheld), word+" settings have no such tag")
	}
}
