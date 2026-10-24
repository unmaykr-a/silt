package store_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// Two queries answering one question are worth exactly as much as the thing
// checking that they agree.
//
// The project-scoped range reads were split off so SQLite can use the index led
// by project_id -- an OR on that column leaves it nothing to lead with, so the
// one-query form scanned the window across every project and filtered. The
// split is only safe while both forms return the same rows, which is what this
// compares: same data, same window, same filters, row for row.

func seedRanges(t *testing.T, db *store.Store) (projectIDs []int64, from, to int64) {
	t.Helper()
	ctx := t.Context()
	host, err := db.Q.UpsertHost(ctx, sqlcgen.UpsertHostParams{
		Name: "pi", Endpoint: "tcp://x", CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatalf("host: %v", err)
	}

	base := store.Now() - 100000
	from, to = base-1, base+100000

	types := []string{"container.start", "container.die", "image.pull"}
	severities := []string{"info", "error", "warn"}
	services := []string{"radarr", "sonarr"}

	for p := 0; p < 4; p++ {
		project, err := db.Q.UpsertProject(ctx, sqlcgen.UpsertProjectParams{
			HostID: host.ID, Name: fmt.Sprintf("p%d", p), ConfigFiles: "[]",
			FirstSeenAt: base, LastSeenAt: base,
		})
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		projectIDs = append(projectIDs, project.ID)

		for i := 0; i < 12; i++ {
			id := project.ID
			if _, err := db.RecordEvent(ctx, store.EventRecord{
				HostID: &host.ID, ProjectID: &id,
				Service:  services[i%len(services)],
				TS:       base + int64(i*500) + project.ID,
				Source:   store.SourceDocker,
				Type:     types[i%len(types)],
				Severity: severities[i%len(severities)],
				Message:  "m",
			}); err != nil {
				t.Fatalf("event: %v", err)
			}
		}
	}

	// An event belonging to no project at all. Asking for project zero must
	// mean "every project" and not "the rows whose project is null", and the
	// split must not change which of those it means.
	if _, err := db.RecordEvent(ctx, store.EventRecord{
		HostID: &host.ID, TS: base + 7, Source: store.SourceWebhook,
		Type: "monitor.down", Severity: "error", Message: "hostless",
	}); err != nil {
		t.Fatalf("hostless event: %v", err)
	}
	return projectIDs, from, to
}

// firstDifference names where two result sets part company. Comparing lengths
// alone reports "12 rows and 12 rows" when the only difference is the order,
// which is a failure message that sends the reader looking in the wrong place.
func firstDifference(gotLen, wantLen int, sameAt func(int) bool) string {
	if gotLen != wantLen {
		return fmt.Sprintf("the split query returned %d rows, the combined one %d", gotLen, wantLen)
	}
	for i := 0; i < gotLen; i++ {
		if !sameAt(i) {
			return fmt.Sprintf("the two queries agree on %d rows then differ at row %d", i, i)
		}
	}
	return ""
}

func TestTheSplitEventQueryAgreesWithTheCombinedOne(t *testing.T) {
	db, _ := openTestStore(t)
	projects, from, to := seedRanges(t, db)

	filters := []struct {
		name                   string
		service, typ, severity string
	}{
		{name: "no filters"},
		{name: "service", service: "radarr"},
		{name: "type", typ: "container.die"},
		{name: "severity", severity: "error"},
		{name: "every filter at once", service: "sonarr", typ: "image.pull", severity: "warn"},
	}

	for _, id := range append([]int64{0}, projects...) {
		for _, f := range filters {
			params := sqlcgen.ListEventsParams{
				FromTs: from, ToTs: to, ProjectID: id,
				Service: f.service, Type: f.typ, Severity: f.severity, MaxRows: 100,
			}
			want, err := db.RQ.ListEvents(t.Context(), params)
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			got, err := db.EventsInRange(t.Context(), params)
			if err != nil {
				t.Fatalf("EventsInRange: %v", err)
			}
			if diff := firstDifference(len(got), len(want), func(i int) bool { return got[i].ID == want[i].ID }); diff != "" {
				t.Errorf("project %d, %s: %s", id, f.name, diff)
			} else if !reflect.DeepEqual(got, want) {
				t.Errorf("project %d, %s: same ids in the same order but the rows differ", id, f.name)
			}
			if id != 0 && len(got) == 0 {
				t.Errorf("project %d, %s: nothing matched, so this case proves nothing", id, f.name)
			}
		}
	}
}

func TestTheSplitSnapshotQueryAgreesWithTheCombinedOne(t *testing.T) {
	db, redactor := openTestStore(t)
	projects, from, to := seedRanges(t, db)

	// Snapshots need real observations behind them.
	for _, id := range projects {
		for i := 0; i < 3; i++ {
			obs := observation(t, redactor, serviceOpts{
				image: fmt.Sprintf("example/app:%d", i), state: "running",
			})
			if _, err := db.WriteSnapshot(t.Context(), id, from+int64(i*1000)+id, "manual", obs); err != nil {
				t.Fatalf("snapshot: %v", err)
			}
		}
	}

	for _, id := range append([]int64{0}, projects...) {
		params := sqlcgen.SnapshotsInRangeParams{
			FromTs: from, ToTs: to, ProjectID: id, MaxRows: 100,
		}
		want, err := db.RQ.SnapshotsInRange(t.Context(), params)
		if err != nil {
			t.Fatalf("SnapshotsInRange: %v", err)
		}
		got, err := db.SnapshotsInWindow(t.Context(), params)
		if err != nil {
			t.Fatalf("SnapshotsInWindow: %v", err)
		}
		if diff := firstDifference(len(got), len(want), func(i int) bool { return got[i].ID == want[i].ID }); diff != "" {
			t.Errorf("project %d: %s", id, diff)
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("project %d: same ids in the same order but the rows differ", id)
		}
		if id != 0 && len(got) == 0 {
			t.Errorf("project %d: nothing matched, so this case proves nothing", id)
		}
	}
}
