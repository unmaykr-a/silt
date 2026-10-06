package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

type eventDetail struct {
	Event struct {
		ID       int64           `json:"id"`
		Type     string          `json:"type"`
		Severity string          `json:"severity"`
		Service  string          `json:"service"`
		Message  string          `json:"message"`
		Payload  json.RawMessage `json:"payload"`
	} `json:"event"`
	Project *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Host           string `json:"host"`
	PreviousChange *struct {
		SnapshotID int64 `json:"snapshot_id"`
		ProjectID  int64 `json:"project_id"`
		TakenAt    int64 `json:"taken_at"`
		BeforeMS   int64 `json:"before_ms"`
	} `json:"previous_change"`
	Nearby []struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
		TS   int64  `json:"ts"`
	} `json:"nearby"`
	WindowFrom int64 `json:"window_from"`
	WindowTo   int64 `json:"window_to"`
}

func (f *fixture) eventDetail(t *testing.T, id int64) eventDetail {
	t.Helper()
	resp, body := f.get(t, "/api/events/"+strconv.FormatInt(id, 10))
	if resp.StatusCode != 200 {
		t.Fatalf("GET event %d = %d %s", id, resp.StatusCode, body)
	}
	var out eventDetail
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode event detail: %v (%s)", err, body)
	}
	return out
}

// A feed row said `03:00 container.die` and nothing else. This is the endpoint
// that answers the rest, so it has to carry the things the row could not: the
// detail the engine sent, and the names behind the ids.
func TestAnEventCarriesItsDetailAndItsNames(t *testing.T) {
	f := newFixture(t)
	d := f.eventDetail(t, f.eventID)

	if d.Event.Type != "container.die" {
		t.Fatalf("type = %q", d.Event.Type)
	}
	// The exit code is the whole reason the capture path changed. A die with
	// no exit status is the event this feature exists for.
	var payload map[string]any
	if err := json.Unmarshal(d.Event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got := payload["exit_code"]; got != "137" {
		t.Errorf("exit_code = %v, want 137", got)
	}
	if got := payload["withheld_attributes"]; got == nil {
		t.Error("nothing says how many attributes were withheld, so the panel would imply this is everything the engine sent")
	}

	// The names, so a screen rendering one event does not need the whole
	// project list to say which project it was.
	if d.Project == nil || d.Project.Name != "media" {
		t.Errorf("project = %+v, want media", d.Project)
	}
	if d.Host == "" {
		t.Error("no host name on the event")
	}
}

// The question Silt exists for: the image that got pulled at 03:00, beside the
// thing that died at 03:10. From a single event that was unreachable.
func TestAnEventLinksToTheChangeBeforeIt(t *testing.T) {
	f := newFixture(t)
	d := f.eventDetail(t, f.eventID)

	if d.PreviousChange == nil {
		t.Fatal("no previous change; the fixture has two snapshots and the second changed")
	}
	if d.PreviousChange.SnapshotID == 0 {
		t.Error("the previous change has no snapshot to open")
	}
	if d.PreviousChange.ProjectID != f.projectID {
		t.Errorf("previous change project = %d, want %d", d.PreviousChange.ProjectID, f.projectID)
	}
	// Reported as a gap rather than leaving the reader to subtract two epoch
	// milliseconds: three minutes is a lead, three days is a coincidence.
	if d.PreviousChange.BeforeMS < 0 {
		t.Errorf("before_ms = %d, want the change to precede the event", d.PreviousChange.BeforeMS)
	}
}

// Nearby is what else was happening. The window is reported with it, because a
// list of three events implies there were three unless something says over what
// span.
func TestAnEventReportsTheWindowItsNeighboursCameFrom(t *testing.T) {
	f := newFixture(t)
	d := f.eventDetail(t, f.eventID)

	if d.WindowFrom >= d.WindowTo {
		t.Errorf("window = [%d, %d], want a span", d.WindowFrom, d.WindowTo)
	}
	if d.Event.ID == 0 {
		t.Fatal("no event id")
	}
	for _, n := range d.Nearby {
		if n.ID == d.Event.ID {
			t.Error("the event is listed among its own neighbours")
		}
		if n.TS < d.WindowFrom || n.TS > d.WindowTo {
			t.Errorf("neighbour at %d is outside the reported window [%d, %d]", n.TS, d.WindowFrom, d.WindowTo)
		}
	}
}

func TestAnEventThatDoesNotExistIs404(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/events/999999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET a missing event = %d %s, want 404", resp.StatusCode, body)
	}
}

// A viewer reads every screen, and this is a screen.
func TestAViewerCanOpenAnEvent(t *testing.T) {
	url := viewerServer(t)
	client := &http.Client{}
	viewer := map[string]string{"X-Remote-User": "reader", "X-Remote-Groups": "users"}

	// Any id: the role check runs before the lookup, so a 404 proves it was
	// not the role that refused.
	code, body := status(t, client, http.MethodGet, url+"/api/events/1", viewer, "")
	if code == http.StatusForbidden {
		t.Errorf("a viewer was refused an event: %s", body)
	}
}
