package store

import (
	"context"
	"database/sql"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// The two range reads behind the timeline, each dispatched to the query whose
// shape the database can index.
//
// Both tables carry a composite index led by project_id and the filters are
// optional, which reads well in one query and costs an index: SQLite cannot use
// a leading column that sits behind an OR, so a project-scoped range scanned
// the window across every project and filtered the rest away. Splitting the
// query is the fix; keeping the choice here is so the four call sites do not
// each have to remember it.
//
// The pair is covered by a test that runs both forms over the same data and
// compares the rows, because two queries claiming to answer the same question
// are only as good as the thing checking that they do.

// EventsInRange lists events in a window, by the index the filters allow.
func (s *Store) EventsInRange(ctx context.Context, p sqlcgen.ListEventsParams) ([]sqlcgen.Event, error) {
	if p.ProjectID == 0 {
		return s.RQ.ListEvents(ctx, p)
	}
	return s.RQ.ListProjectEvents(ctx, sqlcgen.ListProjectEventsParams{
		// Valid because project_id is nullable on events: an event that belongs
		// to no project has a null there, and asking for project zero must find
		// none of them rather than all of them.
		ProjectID: sql.NullInt64{Int64: p.ProjectID, Valid: true},
		FromTs:    p.FromTs,
		ToTs:      p.ToTs,
		Service:   p.Service,
		Type:      p.Type,
		Severity:  p.Severity,
		MaxRows:   p.MaxRows,
	})
}

// SnapshotsInWindow lists snapshots in a window, by the index the filter allows.
func (s *Store) SnapshotsInWindow(ctx context.Context, p sqlcgen.SnapshotsInRangeParams) ([]sqlcgen.Snapshot, error) {
	if p.ProjectID == 0 {
		return s.RQ.SnapshotsInRange(ctx, p)
	}
	return s.RQ.SnapshotsInRangeForProject(ctx, sqlcgen.SnapshotsInRangeForProjectParams{
		ProjectID: p.ProjectID,
		FromTs:    p.FromTs,
		ToTs:      p.ToTs,
		MaxRows:   p.MaxRows,
	})
}
