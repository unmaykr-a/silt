package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// The fleet view: every project on a host, with enough state to answer "which
// of these needs me right now" without opening any of them.
//
// The Projects screen was a card per stack carrying its name and when it was
// last seen. On a host running forty-odd of them that is forty-odd cards all
// saying "2m ago", which is a directory rather than an answer. What someone
// actually wants at a glance is: what is down, what is unhealthy, what has been
// restarting, and what did I edit and forget to apply.
//
// Hand-written for the reason PROJECT.md Section 15 records: sqlc's SQLite
// grammar. This is three set-based queries joined in Go rather than a query per
// project — a per-project loop is forty-seven round trips to answer one screen,
// and it would grow with the host.

// ProjectOverview is one stack, summarised.
type ProjectOverview struct {
	ID         int64
	Name       string
	WorkingDir string
	Archived   bool
	LastSeenAt int64

	// SnapshotID is the latest snapshot, and the one every count below was
	// read from. Zero when the project has never been snapshotted.
	SnapshotID int64
	TakenAt    int64

	// LastChangedAt is when the configuration last actually changed, which is
	// a different question from when the project was last observed. Zero when
	// it has never changed since Silt first saw it.
	LastChangedAt int64

	Services int
	Running  int

	// The ways a container can fail to be running, kept apart rather than
	// summed into one "stopped" count. They are different problems and one of
	// them is not a problem at all: a container someone stopped on purpose and
	// a container in a crash loop have nothing in common except that neither
	// is running, and lumping them together was the reason the screen could
	// not tell you which you were looking at.
	Stopped    int // exited or dead
	Restarting int // crash-looping, or coming back up
	Paused     int // deliberately suspended
	Starting   int // created, or running with a healthcheck that has not passed
	// Unhealthy counts running containers whose healthcheck is failing, which
	// is a different failure from not running at all — the process is up and
	// answering wrongly.
	Unhealthy int
	// Crashed counts stopped containers with a non-zero exit code: the ones
	// nobody asked to stop.
	Crashed int
	// OOMKilled counts containers the kernel killed for memory. Not derivable
	// from the exit code, since an OOM kill and a `docker kill` are both 137.
	OOMKilled int

	// MaxRestartCount is the highest restart count among the stack's
	// containers, as Docker reports it: restarts since that container was
	// created. It is not a rate and not a window — a container recreated by
	// `up` starts again at zero, so any "restarts in the last day" derived
	// from these counters would go negative exactly when a stack was
	// redeployed.
	MaxRestartCount int

	// Drift means the compose files on disk differ from the ones that were in
	// place the last time the running configuration actually changed: someone
	// edited a file and has not applied it.
	//
	// Derived by comparing two files fingerprints — the latest snapshot's
	// against that of the last snapshot with config_changed — rather than from
	// the snapshot's own files_changed flag or the config.drift event. Both of
	// those answer "did this happen", and go quiet again at the next unrelated
	// container restart; the question on this screen is "is it still true".
	//
	// The one case it reads generously: recreating a single service re-reads
	// the whole file, so a `compose up -d oneservice` marks the project's
	// current file as applied. That is what Compose itself believes.
	Drift bool

	// filesFingerprint is the latest snapshot's, kept only long enough to
	// compare against the last applied one.
	filesFingerprint string
}

// Attention reports whether this project is one to look at.
//
// Drift is included deliberately: nothing is broken, which is exactly why it
// is easy to lose. An edited compose file that was never applied is a change
// that will land at the next unrelated restart, hours or weeks later.
func (p ProjectOverview) Attention() bool {
	return p.Unhealthy > 0 || p.Crashed > 0 || p.OOMKilled > 0 ||
		p.Restarting > 0 || p.Drift || p.MaxRestartCount > 0
}

// Overview reads every project on a host with its current state.
func (s *Store) Overview(ctx context.Context, hostID int64) ([]ProjectOverview, error) {
	projects, bySnapshot, err := s.overviewProjects(ctx, hostID)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return projects, nil
	}

	ids := make([]int64, 0, len(bySnapshot))
	for id := range bySnapshot {
		ids = append(ids, id)
	}
	if err := s.overviewServiceCounts(ctx, ids, bySnapshot); err != nil {
		return nil, err
	}
	if err := s.overviewLastChanged(ctx, hostID, projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// overviewProjects reads the projects and their latest snapshot in one pass,
// returning an index from snapshot id to the project it belongs to so the
// service counts can be scattered back without a second lookup per row.
func (s *Store) overviewProjects(ctx context.Context, hostID int64) ([]ProjectOverview, map[int64]*ProjectOverview, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT p.id, p.name, p.working_dir, p.archived, p.last_seen_at,
		       s.id, s.taken_at, s.files_fingerprint
		FROM projects p
		LEFT JOIN snapshots s ON s.id = (
			SELECT id FROM snapshots
			WHERE project_id = p.id
			ORDER BY taken_at DESC
			LIMIT 1
		)
		WHERE p.host_id = ?
		ORDER BY p.name`, hostID)
	if err != nil {
		return nil, nil, fmt.Errorf("read project overview: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// The slice owns the values and the map points into it, so counts written
	// through the map are visible to the caller.
	out := []ProjectOverview{}
	type pending struct {
		index      int
		snapshotID int64
	}
	var links []pending

	for rows.Next() {
		var p ProjectOverview
		var archived int64
		var snapID, takenAt sql.NullInt64
		var filesFP sql.NullString
		if err := rows.Scan(
			&p.ID, &p.Name, &p.WorkingDir, &archived, &p.LastSeenAt,
			&snapID, &takenAt, &filesFP,
		); err != nil {
			return nil, nil, fmt.Errorf("scan project overview: %w", err)
		}
		p.Archived = archived != 0
		if snapID.Valid {
			p.SnapshotID = snapID.Int64
			p.TakenAt = takenAt.Int64
			p.filesFingerprint = filesFP.String
			links = append(links, pending{index: len(out), snapshotID: snapID.Int64})
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	bySnapshot := make(map[int64]*ProjectOverview, len(links))
	for _, l := range links {
		bySnapshot[l.snapshotID] = &out[l.index]
	}
	return out, bySnapshot, nil
}

// overviewServiceCounts counts states across every latest snapshot at once.
func (s *Store) overviewServiceCounts(ctx context.Context, ids []int64, bySnapshot map[int64]*ProjectOverview) error {
	if len(ids) == 0 {
		return nil
	}
	// SQLite's parameter limit is in the hundreds by default and a host can
	// hold more projects than that, so the ids go in in chunks rather than as
	// one IN list that works until it doesn't.
	const chunk = 200
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		batch := ids[start:end]

		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		query := `
			SELECT snapshot_id, state, health, restart_count, exit_code, oom_killed
			FROM service_states
			WHERE snapshot_id IN (?` + strings.Repeat(",?", len(batch)-1) + `)`

		if err := s.scanServiceCounts(ctx, query, args, bySnapshot); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) scanServiceCounts(ctx context.Context, query string, args []any, bySnapshot map[int64]*ProjectOverview) error {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("read service counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var snapshotID, restarts, oomKilled int64
		var exitCode sql.NullInt64
		var state, health string
		if err := rows.Scan(&snapshotID, &state, &health, &restarts, &exitCode, &oomKilled); err != nil {
			return fmt.Errorf("scan service count: %w", err)
		}
		p := bySnapshot[snapshotID]
		if p == nil {
			continue
		}
		p.Services++

		switch state {
		case "running":
			p.Running++
			// An empty health is a container with no healthcheck. That is not
			// the same as a healthy one and must not be counted as unhealthy
			// either — most images ship without one.
			switch health {
			case "unhealthy":
				p.Unhealthy++
			case "starting":
				p.Starting++
			}
		case "restarting":
			p.Restarting++
		case "paused":
			p.Paused++
		case "created":
			p.Starting++
		default:
			// exited, dead, removing, and anything a future Docker adds.
			p.Stopped++
			if exitCode.Valid && exitCode.Int64 != 0 {
				p.Crashed++
			}
			if oomKilled != 0 {
				p.OOMKilled++
			}
		}

		if int(restarts) > p.MaxRestartCount {
			p.MaxRestartCount = int(restarts)
		}
	}
	return rows.Err()
}

// overviewLastChanged finds, per project, the last snapshot that recorded an
// actual configuration change — when it happened, and which compose files were
// on disk at the time, which is what "applied" means.
func (s *Store) overviewLastChanged(ctx context.Context, hostID int64, projects []ProjectOverview) error {
	rows, err := s.read.QueryContext(ctx, `
		SELECT s.project_id, s.taken_at, s.files_fingerprint
		FROM snapshots s
		JOIN projects p ON p.id = s.project_id
		WHERE p.host_id = ? AND s.config_changed = 1
		  AND s.taken_at = (
			SELECT MAX(taken_at) FROM snapshots
			WHERE project_id = s.project_id AND config_changed = 1
		  )`, hostID)
	if err != nil {
		return fmt.Errorf("read last change: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type applied struct {
		at          int64
		fingerprint string
	}
	changed := map[int64]applied{}
	for rows.Next() {
		var projectID, takenAt int64
		var fingerprint string
		if err := rows.Scan(&projectID, &takenAt, &fingerprint); err != nil {
			return fmt.Errorf("scan last change: %w", err)
		}
		changed[projectID] = applied{at: takenAt, fingerprint: fingerprint}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range projects {
		p := &projects[i]
		last, ok := changed[p.ID]
		if !ok {
			// Retention can prune the snapshot that applied the current
			// configuration. Without it there is nothing to compare against,
			// and inventing drift from an absence would cry wolf on every old
			// project at once.
			continue
		}
		p.LastChangedAt = last.at
		p.Drift = p.filesFingerprint != "" && last.fingerprint != "" &&
			p.filesFingerprint != last.fingerprint
	}
	return nil
}
