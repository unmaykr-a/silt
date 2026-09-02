package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type auditLog struct {
	Entries []struct {
		ID     int64          `json:"id"`
		TS     int64          `json:"ts"`
		Actor  string         `json:"actor"`
		Method string         `json:"method"`
		Action string         `json:"action"`
		OK     bool           `json:"ok"`
		Detail map[string]any `json:"detail"`
		Remote string         `json:"remote"`
	} `json:"entries"`
	Total int64 `json:"total"`
}

func (f *fixture) audit(t *testing.T) auditLog {
	t.Helper()
	resp, body := f.get(t, "/api/audit")
	if resp.StatusCode != 200 {
		t.Fatalf("audit = %d %s", resp.StatusCode, body)
	}
	var out auditLog
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func (l auditLog) find(action string) (int, bool) {
	for i, e := range l.Entries {
		if e.Action == action {
			return i, true
		}
	}
	return 0, false
}

func TestAuditStartsEmptyAndReturnsAnArray(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/audit")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// A null here is `entries.map is not a function` in the browser.
	if !strings.Contains(string(body), `"entries":[]`) {
		t.Errorf("entries is not an empty array: %s", body)
	}
}

func TestAuditRecordsASettingsChange(t *testing.T) {
	f := newFixture(t)
	resp, body := f.do(t, http.MethodPut, "/api/settings", `{"retention_days":30}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}

	log := f.audit(t)
	i, ok := log.find("settings.changed")
	if !ok {
		t.Fatalf("no settings.changed entry: %+v", log.Entries)
	}
	entry := log.Entries[i]
	if !entry.OK {
		t.Error("a successful change recorded as failed")
	}
	if entry.TS == 0 {
		t.Error("entry has no timestamp")
	}
	// The field name is the useful part, and the only part it is safe to keep.
	fields, _ := entry.Detail["fields"].([]any)
	found := false
	for _, f := range fields {
		if f == "retention_days" {
			found = true
		}
	}
	if !found {
		t.Errorf("detail does not name the changed field: %+v", entry.Detail)
	}
	if log.Total < 1 {
		t.Errorf("total = %d, want at least 1", log.Total)
	}
}

// The trail is a table built to be read, and the settings screen holds an
// ingest token and notification URLs. Recording what a setting *became* would
// put credentials in the one place designed to be handed out.
func TestAuditNeverRecordsSecretValues(t *testing.T) {
	f := newFixture(t)

	const token = "sentinel-ingest-token-9f3c"
	const target = "gotify://gotify.invalid/SentinelAppToken9f3c"
	resp, body := f.do(t, http.MethodPut, "/api/settings",
		`{"ingest_token":"`+token+`","notify_urls":["`+target+`"]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
	}
	if _, body := f.post(t, "/api/settings/notifications/test", "", nil); len(body) == 0 {
		t.Fatal("no response from the notification test")
	}

	_, raw := f.get(t, "/api/audit")
	for _, secret := range []string{token, target, "SentinelAppToken9f3c"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("audit log leaks %q:\n%s", secret, raw)
		}
	}
	// It should still say that the fields changed.
	log := f.audit(t)
	if _, ok := log.find("settings.changed"); !ok {
		t.Error("the change was not recorded at all")
	}
}

func TestAuditRecordsAPrune(t *testing.T) {
	f := newFixture(t)
	if resp, body := f.post(t, "/api/maintenance/prune", "", nil); resp.StatusCode != 200 {
		t.Fatalf("prune = %d %s", resp.StatusCode, body)
	}
	if _, ok := f.audit(t).find("maintenance.prune"); !ok {
		t.Error("a prune deletes history permanently and was not recorded")
	}
}

func TestAuditRecordsAForcedSnapshot(t *testing.T) {
	f := newFixture(t)
	if resp, _ := f.post(t, "/api/projects/1/snapshot", "", nil); resp.StatusCode != 202 {
		t.Skip("snapshotter unavailable in this fixture")
	}
	if _, ok := f.audit(t).find("maintenance.snapshot"); !ok {
		t.Error("a forced snapshot was not recorded")
	}
}

func TestAuditIsNewestFirst(t *testing.T) {
	f := newFixture(t)
	for _, days := range []string{"30", "31", "32"} {
		if resp, body := f.do(t, http.MethodPut, "/api/settings", `{"retention_days":`+days+`}`, nil); resp.StatusCode != 200 {
			t.Fatalf("PUT settings = %d %s", resp.StatusCode, body)
		}
	}
	log := f.audit(t)
	if len(log.Entries) < 3 {
		t.Fatalf("entries = %d, want at least 3", len(log.Entries))
	}
	for i := 1; i < len(log.Entries); i++ {
		if log.Entries[i-1].TS < log.Entries[i].TS {
			t.Fatalf("entries are not newest-first: %d before %d", log.Entries[i-1].TS, log.Entries[i].TS)
		}
	}
}

// An install with no authentication has nobody to name, and "admin" would read
// as a real account on a Silt where anyone who can reach the port is admin.
func TestAuditLeavesTheActorEmptyWithoutAuth(t *testing.T) {
	f := newFixture(t)
	if resp, _ := f.do(t, http.MethodPut, "/api/settings", `{"retention_days":30}`, nil); resp.StatusCode != 200 {
		t.Fatal("settings change failed")
	}
	log := f.audit(t)
	i, ok := log.find("settings.changed")
	if !ok {
		t.Fatal("no entry")
	}
	if log.Entries[i].Actor != "" {
		t.Errorf("actor = %q, want empty on an install with no authentication", log.Entries[i].Actor)
	}
	if log.Entries[i].Method != "system" {
		t.Errorf("method = %q, want system", log.Entries[i].Method)
	}
}
