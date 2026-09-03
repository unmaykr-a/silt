package redact_test

import (
	"testing"

	"github.com/unmaykr-a/silt/internal/redact"
)

// A keep key decides what is stored in cleartext. Before this was validated,
// `*` was a legal pattern that matched every environment variable on the host
// and silently turned off the redaction Silt is built around.
func TestValidateKeepKeyRefusesPatternsThatKeepEverything(t *testing.T) {
	for _, pattern := range []string{"*", "**", "***", " * ", "?", "??", "[A-Z]*", "*[a-z]", "?*", "*?"} {
		if err := redact.ValidateKeepKey(pattern); err == nil {
			t.Errorf("ValidateKeepKey(%q) allowed a pattern that keeps far more than it names", pattern)
		}
	}
}

func TestValidateKeepKeyAllowsWhatTheDocsPromise(t *testing.T) {
	for _, pattern := range []string{"PUID", "TZ", "APP_*", "*_PORT", "app_region", "MY.KEY", "MY-KEY", "X1"} {
		if err := redact.ValidateKeepKey(pattern); err != nil {
			t.Errorf("ValidateKeepKey(%q) refused a documented pattern: %v", pattern, err)
		}
	}
}

func TestValidateKeepKeyRefusesGlobMetacharacters(t *testing.T) {
	for _, pattern := range []string{"APP[1]_TOKEN", "A*B", "A\\B", "APP]", "A?B"} {
		if err := redact.ValidateKeepKey(pattern); err == nil {
			t.Errorf("ValidateKeepKey(%q) allowed glob syntax the matcher would interpret", pattern)
		}
	}
}

func TestValidateKeepKeyRefusesEmpty(t *testing.T) {
	if err := redact.ValidateKeepKey("   "); err == nil {
		t.Error("an empty keep key was allowed")
	}
}

// The end that matters: a pattern that slipped through must not widen the
// keep-list. Dropping it fails closed — the key stays redacted.
func TestKeepListIgnoresAnInvalidPattern(t *testing.T) {
	r := redact.New([]byte("key"), []string{"*", "[A-Z]*", "APP_*"})
	for _, key := range []string{"DB_PASSWORD", "API_KEY", "SECRET"} {
		if r.Keep(key) {
			t.Errorf("%s was kept in cleartext by an invalid catch-all pattern", key)
		}
	}
	if !r.Keep("APP_REGION") {
		t.Error("the one valid pattern in the list stopped working")
	}
}

func TestValidateKeepKeys(t *testing.T) {
	if err := redact.ValidateKeepKeys([]string{"APP_*", "", "  ", "TZ"}); err != nil {
		t.Errorf("a valid list was refused: %v", err)
	}
	if err := redact.ValidateKeepKeys([]string{"APP_*", "*"}); err == nil {
		t.Error("a list containing a catch-all was accepted")
	}
}
