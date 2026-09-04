package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The export is the override document, portable. The thing worth pinning is
// what it refuses to carry: a shoutrrr URL holds the credential for the
// service it points at, and the ingest token is a credential outright. Neither
// is readable anywhere else in the API, and neither becomes readable by being
// called an export.

func TestExportOmitsSecretsAndSaysSo(t *testing.T) {
	f := newFixture(t)

	// Set one of each kind: two secrets and one ordinary value.
	if resp, body := f.do(t, http.MethodPut, "/api/settings",
		`{"notify_urls":["ntfy://ntfy.sh/secret-topic"],"ingest_token":"s3cr3t","log_level":"debug"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("PUT /api/settings = %d: %s", resp.StatusCode, body)
	}

	resp, raw := f.get(t, "/api/settings/export")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/settings/export = %d: %s", resp.StatusCode, raw)
	}
	body := string(raw)

	for _, secret := range []string{"secret-topic", "s3cr3t", "ntfy://"} {
		if strings.Contains(body, secret) {
			t.Errorf("the export carries %q:\n%s", secret, body)
		}
	}

	var out struct {
		Silt     string          `json:"silt"`
		Settings json.RawMessage `json:"settings"`
		Omitted  []string        `json:"omitted"`
		Note     string          `json:"note"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Silt != "settings" {
		t.Errorf("silt = %q, want \"settings\"", out.Silt)
	}
	// Named rather than silently dropped: a file that quietly omits your
	// notification targets is a restore that quietly stops notifying.
	want := map[string]bool{"notify_urls": true, "ingest_token": true}
	for _, name := range out.Omitted {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Errorf("omitted = %v, does not name %v", out.Omitted, want)
	}
	// And the ordinary value survives, or the export is useless.
	if !strings.Contains(string(out.Settings), `"log_level":"debug"`) {
		t.Errorf("the export lost a non-secret override: %s", out.Settings)
	}
	if out.Note == "" {
		t.Error("the export has no note explaining what it is")
	}
}

func TestExportRoundTripsThroughTheOrdinaryPatchEndpoint(t *testing.T) {
	// There is no import endpoint on purpose: the export is already the shape
	// PUT /api/settings takes. A second write path would be a second set of
	// validation rules to keep in step.
	f := newFixture(t)

	if resp, _ := f.do(t, http.MethodPut, "/api/settings", `{"log_level":"debug","retention_days":42}`, nil); resp.StatusCode != 200 {
		t.Fatal("seed write failed")
	}
	_, exported := f.get(t, "/api/settings/export")

	var doc struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(exported, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Back to the environment, then restore from the file.
	if resp, body := f.do(t, http.MethodDelete, "/api/settings", "", nil); resp.StatusCode != 200 {
		t.Fatalf("DELETE /api/settings = %d: %s", resp.StatusCode, body)
	}
	if resp, body := f.do(t, http.MethodPut, "/api/settings", string(doc.Settings), nil); resp.StatusCode != 200 {
		t.Fatalf("restore = %d: %s", resp.StatusCode, body)
	}

	_, after := f.get(t, "/api/settings")
	if !strings.Contains(string(after), `"retention_days":42`) {
		t.Errorf("the restore did not take:\n%s", after)
	}
}

func TestTheExportFilenameSurvivesAnAwkwardHostName(t *testing.T) {
	// SILT_HOST_NAME is a free-text label and a header value is not: a quote
	// closes the filename early, a newline is not allowed in a header at all.
	// Nothing here is an attack — the value is the operator's own — but naming
	// your host `my "prod" box` should not break the download.
	f := newFixtureWithHostName(t, `my "prod" box/../etc`)

	resp, _ := f.get(t, "/api/settings/export")
	got := resp.Header.Get("Content-Disposition")
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("the header carries a line break: %q", got)
	}
	if got != `attachment; filename="silt-settings-my--prod--box----etc.json"` {
		t.Errorf("Content-Disposition = %q", got)
	}
}
