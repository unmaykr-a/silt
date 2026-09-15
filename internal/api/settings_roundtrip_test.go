package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/config"
)

// wireValues is a valid value for every editable setting, in the JSON a save
// would send.
//
// Hand-maintained, and TestEverySettingHasARoundTripValue fails when a setting
// is tagged without one. That is deliberate: the recurring failure in this
// project is a setting that exists in the config struct, the example
// environment and the manual, and is wired to nothing — declaring it is the
// easy half. A setting is not finished until a value typed into the UI comes
// back out of the API, and the only way to know that is to send one.
//
// The values are mutually consistent, so they can all be sent at once:
// unchanged retention stays under changed retention, and every interval clears
// its lower bound.
var wireValues = map[string]string{
	"log_level":                `"debug"`,
	"snapshot_interval_ms":     `60000`,
	"retention_days":           `30`,
	"unchanged_retention_days": `5`,
	"event_retention_days":     `14`,
	"audit_retention_days":     `100`,
	"vacuum_interval_ms":       `86400000`,
	"retention_interval_ms":    `1800000`,
	"keep_keys":                `["TZ","PUID"]`,
	"ingest_token":             `"round-trip-token-value"`,
	"notify_urls":              `["generic://example.invalid/hook"]`,
	"notify_on":                `["image_id"]`,
	"notify_min_severity":      `"high"`,
	"base_url":                 `"https://silt.example"`,
}

// TestEverySettingHasARoundTripValue keeps the table above complete in both
// directions: a setting with no value here is untested, and a value here for a
// setting that no longer exists is a stale entry that would never run.
func TestEverySettingHasARoundTripValue(t *testing.T) {
	for _, name := range config.EditableNames() {
		if _, ok := wireValues[name]; !ok {
			t.Errorf("editable setting %q has no value in wireValues, so nothing tests that it round-trips", name)
		}
	}
	for name := range wireValues {
		if _, ok := config.EditableField(name); !ok {
			t.Errorf("wireValues has %q, which is not an editable setting", name)
		}
	}
}

// TestEverySettingRoundTripsThroughTheAPI is the check that would have caught
// every half-built setting in this project's history: it runs from the HTTP
// patch to the value the next reader gets back, per setting, generically.
//
// A secret is the one case that cannot be compared, because it is deliberately
// never returned. So the assertion inverts: the value must not appear anywhere
// in the response.
func TestEverySettingRoundTripsThroughTheAPI(t *testing.T) {
	for _, f := range config.Editable() {
		t.Run(f.Name, func(t *testing.T) {
			fx := newFixture(t)
			value := wireValues[f.Name]

			resp, body := fx.do(t, http.MethodPut, "/api/settings", `{"`+f.Name+`":`+value+`}`, nil)
			if resp.StatusCode != 200 {
				t.Fatalf("PUT %s = %d %s", f.Name, resp.StatusCode, body)
			}

			after := fx.settings(t)
			if !slices.Contains(after.Overridden, f.Name) {
				t.Errorf("after setting %s, overridden = %v: the screen would not show it as set here",
					f.Name, after.Overridden)
			}

			effective := effectiveOf(t, fx)
			if f.Secret {
				var want string
				if err := json.Unmarshal([]byte(value), &want); err == nil && want != "" {
					if strings.Contains(effective, want) {
						t.Errorf("secret setting %s was echoed back by GET /api/settings", f.Name)
					}
				}
				var list []string
				if err := json.Unmarshal([]byte(value), &list); err == nil {
					for _, item := range list {
						if strings.Contains(effective, item) {
							t.Errorf("secret setting %s leaked %q through GET /api/settings", f.Name, item)
						}
					}
				}
				return
			}

			got := rawField(t, effective, f.Name)
			if got == nil {
				t.Fatalf("setting %s is writable but GET /api/settings does not return it: the settings form would reset the control every time it opened", f.Name)
			}
			if !sameJSON(t, got, []byte(value)) {
				t.Errorf("%s: sent %s, read back %s", f.Name, value, got)
			}
		})
	}
}

// TestEverySettingIsReadableBack is the same "can be written but never read"
// guard without a write, so it fails for a whole class of setting rather than
// one at a time.
func TestEverySettingIsReadableBack(t *testing.T) {
	fx := newFixture(t)
	effective := effectiveOf(t, fx)
	for _, f := range config.Editable() {
		if f.Secret {
			continue
		}
		if rawField(t, effective, f.Name) == nil {
			t.Errorf("editable setting %q is not a key in the effective settings payload", f.Name)
		}
	}
}

// TestEverySettingResetsToTheEnvironment covers the other half of the contract:
// an override can be taken back. Every setting is overridden at once first, so
// this also proves the values in wireValues validate together rather than only
// one at a time.
func TestEverySettingResetsToTheEnvironment(t *testing.T) {
	fx := newFixture(t)

	var parts []string
	for _, f := range config.Editable() {
		parts = append(parts, `"`+f.Name+`":`+wireValues[f.Name])
	}
	if resp, body := fx.do(t, http.MethodPut, "/api/settings", "{"+strings.Join(parts, ",")+"}", nil); resp.StatusCode != 200 {
		t.Fatalf("PUT every setting = %d %s", resp.StatusCode, body)
	}
	if got := len(fx.settings(t).Overridden); got != len(config.Editable()) {
		t.Fatalf("overridden %d settings, want all %d", got, len(config.Editable()))
	}

	names := config.EditableNames()
	reset, err := json.Marshal(map[string]any{"reset": names})
	if err != nil {
		t.Fatalf("encode reset: %v", err)
	}
	if resp, body := fx.do(t, http.MethodPut, "/api/settings", string(reset), nil); resp.StatusCode != 200 {
		t.Fatalf("reset every setting = %d %s", resp.StatusCode, body)
	}
	if got := fx.settings(t).Overridden; len(got) != 0 {
		t.Errorf("after resetting everything, overridden = %v", got)
	}
}

// effectiveOf returns the effective half of the settings payload as raw JSON,
// which is how these tests reach settings the typed test struct does not name.
func effectiveOf(t *testing.T, fx *fixture) string {
	t.Helper()
	resp, body := fx.get(t, "/api/settings")
	if resp.StatusCode != 200 {
		t.Fatalf("GET settings = %d %s", resp.StatusCode, body)
	}
	var payload struct {
		Effective json.RawMessage `json:"effective"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return string(payload.Effective)
}

func rawField(t *testing.T, object, name string) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return fields[name]
}

// sameJSON compares values rather than bytes, so an array that came back with
// different spacing is not a failure.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(av, bv)
}
