package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type settingsPayload struct {
	Version   string `json:"version"`
	Release   string `json:"release"`
	Editable  bool   `json:"editable"`
	Effective struct {
		RetentionDays      int      `json:"retention_days"`
		SnapshotIntervalMS int64    `json:"snapshot_interval_ms"`
		KeepKeys           []string `json:"keep_keys"`
		NotifyTargets      []string `json:"notify_targets"`
		IngestConfigured   bool     `json:"ingest_configured"`
		LogLevel           string   `json:"log_level"`
	} `json:"effective"`
	Environment struct {
		RetentionDays int `json:"retention_days"`
	} `json:"environment"`
	Overridden []string `json:"overridden"`
	Fixed      struct {
		DBPath   string `json:"db_path"`
		AuthMode string `json:"auth_mode"`
	} `json:"fixed"`
}

func decodeSettings(t *testing.T, body []byte) settingsPayload {
	t.Helper()
	var out settingsPayload
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode settings: %v (%s)", err, body)
	}
	return out
}

func (f *fixture) settings(t *testing.T) settingsPayload {
	t.Helper()
	resp, body := f.get(t, "/api/settings")
	if resp.StatusCode != 200 {
		t.Fatalf("GET settings = %d %s", resp.StatusCode, body)
	}
	return decodeSettings(t, body)
}

func TestSettingsUpdateTakesEffectAndReportsItsSource(t *testing.T) {
	f := newFixture(t)

	before := f.settings(t)
	if !before.Editable {
		t.Fatal("settings report themselves as read-only")
	}
	if before.Effective.RetentionDays != 365 {
		t.Fatalf("retention = %d, want the environment's 365", before.Effective.RetentionDays)
	}

	resp, body := f.do(t, http.MethodPut, "/api/settings", `{"retention_days":30}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	after := decodeSettings(t, body)
	if after.Effective.RetentionDays != 30 {
		t.Errorf("retention after update = %d, want 30", after.Effective.RetentionDays)
	}
	if after.Environment.RetentionDays != 365 {
		t.Errorf("environment retention = %d; the baseline must not move", after.Environment.RetentionDays)
	}
	if !contains(after.Overridden, "retention_days") {
		t.Errorf("overridden = %v, want retention_days listed", after.Overridden)
	}

	// And it is visible to the next reader, not just echoed by the write.
	if got := f.settings(t).Effective.RetentionDays; got != 30 {
		t.Errorf("retention on re-read = %d, want 30", got)
	}
}

func TestSettingsRejectAnInvalidValueWithoutChangingAnything(t *testing.T) {
	f := newFixture(t)

	resp, body := f.do(t, http.MethodPut, "/api/settings", `{"snapshot_interval_ms":10}`, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("PUT settings = %d %s, want 400", resp.StatusCode, body)
	}
	if got := f.settings(t).Effective.SnapshotIntervalMS; got != 300000 {
		t.Errorf("interval = %d, want the unchanged 300000", got)
	}
}

func TestSettingsRejectAnUnknownSeverity(t *testing.T) {
	f := newFixture(t)
	resp, body := f.do(t, http.MethodPut, "/api/settings", `{"notify_min_severity":"catastrophic"}`, nil)
	if resp.StatusCode != 400 {
		t.Errorf("PUT settings = %d %s, want 400", resp.StatusCode, body)
	}
}

// A shoutrrr URL is the credential for the service it points at. The settings
// screen has to show that targets exist without handing them back, or reading
// the UI would be a way to read the secrets.
func TestSettingsNeverEchoNotificationURLs(t *testing.T) {
	f := newFixture(t)
	const secret = "supersecretwebhooktoken"

	resp, body := f.do(t, http.MethodPut, "/api/settings",
		`{"notify_urls":["gotify://gotify.example/`+secret+`"]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("the write echoed the notification token: %s", body)
	}

	resp, body = f.get(t, "/api/settings")
	if resp.StatusCode != 200 {
		t.Fatalf("GET settings = %d", resp.StatusCode)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("a later read leaked the notification token: %s", body)
	}
	got := decodeSettings(t, body)
	if len(got.Effective.NotifyTargets) != 1 {
		t.Fatalf("notify_targets = %v, want one masked entry", got.Effective.NotifyTargets)
	}
	if !strings.HasPrefix(got.Effective.NotifyTargets[0], "gotify://gotify.example") {
		t.Errorf("masked target = %q, want the scheme and host", got.Effective.NotifyTargets[0])
	}
}

func TestSettingsNeverEchoTheIngestTokenAfterAWrite(t *testing.T) {
	f := newFixture(t)
	const secret = "a-brand-new-ingest-token"

	resp, body := f.do(t, http.MethodPut, "/api/settings", `{"ingest_token":"`+secret+`"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("settings echoed the ingest token: %s", body)
	}
	if !decodeSettings(t, body).Effective.IngestConfigured {
		t.Error("ingest_configured is false after setting a token")
	}
}

// The ingest endpoint must authenticate against the token in force, not the
// one the process started with.
func TestChangingTheIngestTokenTakesEffectImmediately(t *testing.T) {
	f := newFixture(t)
	const next = "rotated-token"

	if resp, body := f.do(t, http.MethodPut, "/api/settings", `{"ingest_token":"`+next+`"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}

	old := map[string]string{"Authorization": "Bearer " + f.ingestTok}
	if resp, _ := f.post(t, "/api/ingest", `{"type":"contract.test"}`, old); resp.StatusCode != 401 {
		t.Errorf("the old token still works: %d", resp.StatusCode)
	}
	fresh := map[string]string{"Authorization": "Bearer " + next}
	if resp, body := f.post(t, "/api/ingest", `{"type":"contract.test"}`, fresh); resp.StatusCode != 202 {
		t.Errorf("the new token was refused: %d %s", resp.StatusCode, body)
	}
}

// Clearing the token must close the endpoint rather than open it: unset never
// means "no authentication required".
func TestClearingTheIngestTokenClosesTheEndpoint(t *testing.T) {
	f := newFixture(t)

	if resp, body := f.do(t, http.MethodPut, "/api/settings", `{"ingest_token":""}`, nil); resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	headers := map[string]string{"Authorization": "Bearer " + f.ingestTok}
	if resp, _ := f.post(t, "/api/ingest", `{"type":"contract.test"}`, headers); resp.StatusCode != 503 {
		t.Errorf("ingest = %d, want 503 once no token is configured", resp.StatusCode)
	}
	if resp, _ := f.post(t, "/api/ingest", `{"type":"contract.test"}`, nil); resp.StatusCode == 202 {
		t.Error("clearing the ingest token left the endpoint open")
	}
}

// The compose root allowlist, the database path and authentication are the
// boundary around this very endpoint. Naming one in a patch must not move it.
func TestFixedSettingsAreNotEditable(t *testing.T) {
	f := newFixture(t)
	before := f.settings(t)

	resp, body := f.do(t, http.MethodPut, "/api/settings",
		`{"db_path":"/tmp/elsewhere.db","compose_roots":["/"],"password_hash":"","trust_proxy_auth":true}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	after := decodeSettings(t, body)
	if after.Fixed.DBPath != before.Fixed.DBPath {
		t.Errorf("db_path moved to %q", after.Fixed.DBPath)
	}
	if after.Fixed.AuthMode != before.Fixed.AuthMode {
		t.Errorf("auth_mode moved to %q", after.Fixed.AuthMode)
	}
	if len(after.Overridden) != 0 {
		t.Errorf("overridden = %v, want nothing: no editable field was named", after.Overridden)
	}
}

func TestResetSettingsReturnsToTheEnvironment(t *testing.T) {
	f := newFixture(t)

	if resp, body := f.do(t, http.MethodPut, "/api/settings", `{"retention_days":30,"base_url":"https://silt.example"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	resp, body := f.do(t, http.MethodDelete, "/api/settings", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("DELETE settings = %d %s", resp.StatusCode, body)
	}
	after := decodeSettings(t, body)
	if len(after.Overridden) != 0 {
		t.Errorf("overridden = %v after a reset", after.Overridden)
	}
	if after.Effective.RetentionDays != 365 {
		t.Errorf("retention = %d, want the environment's 365", after.Effective.RetentionDays)
	}
}

func TestVersionReportsTheChangelog(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/version")
	if resp.StatusCode != 200 {
		t.Fatalf("GET version = %d %s", resp.StatusCode, body)
	}
	var out struct {
		Version  string `json:"version"`
		Release  string `json:"release"`
		Releases []struct {
			Version string `json:"version"`
			Entries []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"entries"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Releases) == 0 {
		t.Fatal("no releases returned")
	}
	if out.Release != out.Releases[0].Version {
		t.Errorf("release = %q but the newest entry is %q", out.Release, out.Releases[0].Version)
	}
	if len(out.Releases[0].Entries) == 0 {
		t.Error("the newest release has no entries")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
