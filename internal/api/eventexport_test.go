package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The export has to be two things at once: skimmable by a person and useful to
// something asked to find a cause in it. Those turn out to be the same
// requirement — lead with counts, then the things that went wrong, then
// everything in order — so these check the shape rather than the prose.

func TestTheExportLeadsWithCountsThenChronology(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/events/export?from=1")
	if resp.StatusCode != 200 {
		t.Fatalf("export = %d %s", resp.StatusCode, body)
	}
	doc := string(body)

	// A file, named, and Markdown — not a JSON blob the browser renders as text.
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", got)
	}
	disp := resp.Header.Get("Content-Disposition")
	if !strings.Contains(disp, "attachment") || !strings.Contains(disp, ".md") {
		t.Errorf("Content-Disposition = %q, want an attachment with an .md name", disp)
	}

	// The order is the feature. Counts before the chronology, because scrolling
	// a thousand lines to find out how many errors there were is the problem
	// the summary solves.
	summary := strings.Index(doc, "## Summary")
	everything := strings.Index(doc, "## Everything, in order")
	if summary < 0 || everything < 0 {
		t.Fatalf("missing sections in:\n%s", doc)
	}
	if summary > everything {
		t.Error("the chronology comes before the summary")
	}

	// The window is stated. A file of events with no window is a file you
	// cannot compare to another one.
	if !strings.Contains(doc, "**Window**") {
		t.Error("the export does not say what window it covers")
	}
	// And the host, so two exports from two installs are distinguishable.
	if !strings.Contains(doc, "test-host") {
		t.Error("the export does not name the host it came from")
	}
}

// The fixture's event is a container.die with an exit code, which is the whole
// point of the capture change: an export that dropped it would be a list of
// things that happened with the reasons removed.
func TestTheExportCarriesTheEngineDetail(t *testing.T) {
	f := newFixture(t)
	_, body := f.get(t, "/api/events/export?from=1")
	doc := string(body)

	if !strings.Contains(doc, "exit_code=137") {
		t.Errorf("the exit code is not in the export:\n%s", doc)
	}
	if !strings.Contains(doc, "withheld=12") {
		t.Error("the export does not say how many attributes were withheld")
	}
	if !strings.Contains(doc, "container.die") {
		t.Error("the event type is missing")
	}
}

// A pipe in a message would end its column early and shift every cell after it,
// which turns one awkward message into a table that cannot be read at all.
func TestAMessageWithAPipeDoesNotBreakTheTable(t *testing.T) {
	f := newFixture(t)
	if resp, body := f.post(t, "/api/ingest",
		`{"type":"probe.fail","message":"curl -s x | grep y failed","severity":"error"}`,
		map[string]string{"Authorization": "Bearer " + f.ingestTok}); resp.StatusCode != 202 {
		t.Fatalf("ingest = %d %s", resp.StatusCode, body)
	}

	_, body := f.get(t, "/api/events/export?from=1")
	doc := string(body)
	if !strings.Contains(doc, `\|`) {
		t.Error("the pipe in the message was not escaped")
	}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.Contains(line, "curl -s x") {
			continue
		}
		// Six columns means seven pipes, and an unescaped one would make eight.
		if got := strings.Count(line, "|") - strings.Count(line, `\|`); got != 7 {
			t.Errorf("row has %d column separators, want 7: %s", got, line)
		}
	}
}

// Filters travel, because an export that quietly meant something other than
// what was on screen would be worse than no export.
func TestTheExportHonoursTheFilters(t *testing.T) {
	f := newFixture(t)

	_, all := f.get(t, "/api/events/export?from=1")
	_, errorsOnly := f.get(t, "/api/events/export?from=1&severity=info")

	if strings.Contains(string(errorsOnly), "container.die") {
		t.Error("filtering to info still returned the error event")
	}
	if !strings.Contains(string(all), "container.die") {
		t.Error("the unfiltered export is missing the error event")
	}
	if !strings.Contains(string(errorsOnly), "**Filters**") {
		t.Error("a filtered export does not say it was filtered, so it reads as the whole window")
	}
}

// JSON for anything that is actually parsing it, where Markdown would be a
// worse CSV. Same content, same summary.
func TestTheJSONExportCarriesTheSameSummary(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/events/export?from=1&format=json")
	if resp.StatusCode != 200 {
		t.Fatalf("export = %d %s", resp.StatusCode, body)
	}
	if disp := resp.Header.Get("Content-Disposition"); !strings.Contains(disp, ".json") {
		t.Errorf("Content-Disposition = %q, want a .json name", disp)
	}

	var out struct {
		Silt     string         `json:"silt"`
		Host     string         `json:"host"`
		Count    int            `json:"count"`
		Severity map[string]int `json:"by_severity"`
		Types    map[string]int `json:"by_type"`
		Events   []struct {
			Type    string          `json:"type"`
			TS      string          `json:"ts"`
			Payload json.RawMessage `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Silt != "events" {
		t.Errorf("silt = %q", out.Silt)
	}
	if out.Count != len(out.Events) {
		t.Errorf("count = %d but %d events", out.Count, len(out.Events))
	}
	if out.Severity["error"] == 0 {
		t.Error("no error count in the summary")
	}
	if out.Types["container.die"] == 0 {
		t.Error("no type count in the summary")
	}
	// Timestamps as readable strings beside the raw milliseconds: the file is
	// read by people as well as parsed.
	if len(out.Events) > 0 && !strings.Contains(out.Events[0].TS, "-") {
		t.Errorf("ts = %q, want a readable timestamp", out.Events[0].TS)
	}
}

// Oldest first. The feed is newest-first because it is read from the top; a
// document is read from the beginning, and an incident only makes sense
// forwards.
func TestTheExportIsOldestFirst(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{
		`{"type":"probe.one","message":"first"}`,
		`{"type":"probe.two","message":"second"}`,
	} {
		if resp, got := f.post(t, "/api/ingest", body,
			map[string]string{"Authorization": "Bearer " + f.ingestTok}); resp.StatusCode != 202 {
			t.Fatalf("ingest = %d %s", resp.StatusCode, got)
		}
	}

	_, raw := f.get(t, "/api/events/export?from=1&format=json")
	var out struct {
		Events []struct {
			TSMS int64 `json:"ts_ms"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := 1; i < len(out.Events); i++ {
		if out.Events[i].TSMS < out.Events[i-1].TSMS {
			t.Fatalf("event %d is older than the one before it", i)
		}
	}
}
