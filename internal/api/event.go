package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// One event, with enough around it to act on.
//
// The feed row was a dead end: `03:00 container.die` and nothing else — no exit
// code, nothing to click, no way to ask what else was happening. Two of those
// three were storage problems and are fixed where the event is captured. This
// is the third: an event is a moment, and a moment is only useful beside what
// came before it.
//
// So the answer to "what is this?" is the row, and the answer to "what do I do
// about it?" is the rest: the change that landed before it, and the events
// either side. Those two are what turn an event into a thread you can pull.

// eventAround is the window searched for neighbouring events. Wide enough to
// catch the restart loop an exit code belongs to, narrow enough that it is
// still "around this" rather than "that evening".
const eventAround = 10 * time.Minute

// eventNeighbours caps how many come back. A crash loop can produce hundreds in
// ten minutes, and a detail panel is not a feed.
const eventNeighbours = 40

type eventDetailResponse struct {
	Event eventResponse `json:"event"`
	// Project and Host are the names behind the ids, so the screen does not
	// have to hold the whole project list to render one event.
	Project *eventProjectRef `json:"project,omitempty"`
	Host    string           `json:"host,omitempty"`
	// PreviousChange is the last configuration change in this project before
	// the event. It is the question Silt exists to answer — the image that got
	// pulled at 03:00, beside the thing that died at 03:10 — and from a single
	// event it was unreachable.
	PreviousChange *eventChangeRef `json:"previous_change,omitempty"`
	// Nearby is what else happened around it, oldest first so it reads as a
	// sequence rather than a list.
	Nearby []eventResponse `json:"nearby"`
	// Window is the span Nearby was taken from, so the screen can say so
	// rather than implying these are all the events there are.
	WindowFrom int64 `json:"window_from"`
	WindowTo   int64 `json:"window_to"`
}

type eventProjectRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type eventChangeRef struct {
	SnapshotID int64 `json:"snapshot_id"`
	ProjectID  int64 `json:"project_id"`
	TakenAt    int64 `json:"taken_at"`
	// BeforeMS is how long before the event it landed, which is the number
	// someone is actually reading for. Three minutes is a lead; three days is
	// a coincidence.
	BeforeMS int64 `json:"before_ms"`
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()

	row, err := s.store.RQ.GetEvent(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read event")
		return
	}

	out := eventDetailResponse{
		Event:      toEventResponse(row),
		Nearby:     []eventResponse{},
		WindowFrom: row.Ts - eventAround.Milliseconds(),
		WindowTo:   row.Ts + eventAround.Milliseconds(),
	}

	if row.ProjectID.Valid {
		if p, err := s.store.RQ.GetProject(ctx, row.ProjectID.Int64); err == nil {
			out.Project = &eventProjectRef{ID: p.ID, Name: p.Name}
		}
		// The change before it. One row: the question is "what changed just
		// before this", and a list of every change this month answers a
		// different one.
		changes, err := s.store.RQ.LatestChangedSnapshotsBefore(ctx, sqlcgen.LatestChangedSnapshotsBeforeParams{
			ProjectID: row.ProjectID.Int64,
			Before:    row.Ts,
			MaxRows:   1,
		})
		if err == nil && len(changes) > 0 {
			out.PreviousChange = &eventChangeRef{
				SnapshotID: changes[0].ID,
				ProjectID:  row.ProjectID.Int64,
				TakenAt:    changes[0].TakenAt,
				BeforeMS:   row.Ts - changes[0].TakenAt,
			}
		}
	}

	// Scoped to the project when there is one. On a forty-project host,
	// "everything in the same ten minutes" is mostly other people's stacks.
	var projectScope int64
	if row.ProjectID.Valid {
		projectScope = row.ProjectID.Int64
	}
	nearby, err := s.store.RQ.EventsAround(ctx, sqlcgen.EventsAroundParams{
		ID:        row.ID,
		FromTs:    out.WindowFrom,
		ToTs:      out.WindowTo,
		ProjectID: projectScope,
		MaxRows:   eventNeighbours,
	})
	if err == nil {
		out.Nearby = toEventResponses(nearby)
	}

	if hosts, err := s.store.RQ.ListHosts(ctx); err == nil && len(hosts) > 0 {
		for _, h := range hosts {
			if row.HostID.Valid && h.ID == row.HostID.Int64 {
				out.Host = h.Name
				break
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}
