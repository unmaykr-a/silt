package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Search across everything Silt records.
//
// Hand-written rather than generated, for the reason PROJECT.md Section 15
// already records twice: sqlc's SQLite grammar has limits, and this hits one.
// It cannot parse `instr(lower(col), sqlc.arg(term))`, and rather than failing
// it silently truncated the statement — `LIMIT ?` came out as `LIM` — so the
// generated code compiled and then failed at runtime. A query that fails at
// runtime instead of at build time is worse than no code generation at all.
//
// A substring match, not LIKE and not FTS5.
//
// Not LIKE, because the terms people type contain its wildcards: `_` is in
// almost every environment key, and sqlc will not parse an ESCAPE clause
// either. instr has no pattern syntax, so SILT_DOCKER_HOST means
// SILT_DOCKER_HOST and there is nothing to escape.
//
// Not FTS5, because these columns are short identifiers, a virtual table is a
// second schema to keep in step with the first, and a tokenizer would split
// SILT_DOCKER_HOST into words nobody searches for. Neither form could use an
// index for an infix match anyway.
//
// lower() is ASCII-only in SQLite, which is what these identifiers are. The
// term is lowered once in Go rather than per row.

// SearchLimit is the per-category cap, applied independently so a term
// matching a thousand events still shows the one project it matched.
const SearchLimit = 25

// ProjectHit is a project whose name matched.
type ProjectHit struct {
	ID         int64
	Name       string
	WorkingDir string
	LastSeenAt int64
	Archived   bool
}

// ServiceHit is a service, and the project to find it in.
type ServiceHit struct {
	Service     string
	ProjectID   int64
	ProjectName string
	LastSeenAt  int64
}

// EnvKeyHit is an environment key, and where it is set.
type EnvKeyHit struct {
	Key         string
	ProjectID   int64
	ProjectName string
	Service     string
	LastSeenAt  int64
	// Readable is true when at least one observation was kept in cleartext,
	// which is what the keep-list decides.
	Readable bool
}

// FileHit is a captured compose or .env file.
type FileHit struct {
	Path        string
	ProjectID   int64
	ProjectName string
	LastSeenAt  int64
}

// EventHit is something that happened.
type EventHit struct {
	ID        int64
	ProjectID sql.NullInt64
	Service   string
	TS        int64
	Source    string
	Type      string
	Severity  string
	Message   string
}

// SearchResults is everything a term matched.
type SearchResults struct {
	Projects []ProjectHit
	Services []ServiceHit
	EnvKeys  []EnvKeyHit
	Files    []FileHit
	Events   []EventHit
}

// MinSearchTerm is the shortest term worth running. One character matches most
// of the database and answers nothing.
const MinSearchTerm = 2

// Search runs every category. A term shorter than MinSearchTerm returns
// nothing rather than everything.
func (s *Store) Search(ctx context.Context, term string, limit int) (SearchResults, error) {
	out := SearchResults{}
	term = strings.ToLower(strings.TrimSpace(term))
	if len([]rune(term)) < MinSearchTerm {
		return out, nil
	}
	if limit <= 0 {
		limit = SearchLimit
	}

	var err error
	if out.Projects, err = s.searchProjects(ctx, term, limit); err != nil {
		return out, err
	}
	if out.Services, err = s.searchServices(ctx, term, limit); err != nil {
		return out, err
	}
	if out.EnvKeys, err = s.searchEnvKeys(ctx, term, limit); err != nil {
		return out, err
	}
	if out.Files, err = s.searchFiles(ctx, term, limit); err != nil {
		return out, err
	}
	if out.Events, err = s.searchEvents(ctx, term, limit); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) searchProjects(ctx context.Context, term string, limit int) ([]ProjectHit, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT id, name, working_dir, last_seen_at, archived
		FROM projects
		WHERE instr(lower(name), ?) > 0
		ORDER BY archived, last_seen_at DESC
		LIMIT ?`, term, limit)
	if err != nil {
		return nil, fmt.Errorf("search projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ProjectHit{}
	for rows.Next() {
		var hit ProjectHit
		var archived int64
		if err := rows.Scan(&hit.ID, &hit.Name, &hit.WorkingDir, &hit.LastSeenAt, &archived); err != nil {
			return nil, fmt.Errorf("scan project hit: %w", err)
		}
		hit.Archived = archived != 0
		out = append(out, hit)
	}
	return out, rows.Err()
}

// searchServices groups by service and project, so a stack observed a thousand
// times is one row rather than a thousand copies of itself.
func (s *Store) searchServices(ctx context.Context, term string, limit int) ([]ServiceHit, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT ss.service, s.project_id, p.name, MAX(s.taken_at)
		FROM service_states ss
		JOIN snapshots s ON s.id = ss.snapshot_id
		JOIN projects p ON p.id = s.project_id
		WHERE instr(lower(ss.service), ?) > 0
		GROUP BY ss.service, s.project_id, p.name
		ORDER BY MAX(s.taken_at) DESC
		LIMIT ?`, term, limit)
	if err != nil {
		return nil, fmt.Errorf("search services: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ServiceHit{}
	for rows.Next() {
		var hit ServiceHit
		if err := rows.Scan(&hit.Service, &hit.ProjectID, &hit.ProjectName, &hit.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan service hit: %w", err)
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// searchEnvKeys finds which projects and services carry a key.
//
// Values are never searched. They are keyed digests, and the cleartext ones
// are on the keep-list precisely because they are not secrets worth hunting
// for — searching them would turn the keep-list into a way to find values by
// their content.
func (s *Store) searchEnvKeys(ctx context.Context, term string, limit int) ([]EnvKeyHit, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT ek.key, s.project_id, p.name, ss.service, MAX(s.taken_at), MIN(ek.redacted)
		FROM env_keys ek
		JOIN service_states ss ON ss.inspect_hash = ek.inspect_hash
		JOIN snapshots s ON s.id = ss.snapshot_id
		JOIN projects p ON p.id = s.project_id
		WHERE instr(lower(ek.key), ?) > 0
		GROUP BY ek.key, s.project_id, p.name, ss.service
		ORDER BY MAX(s.taken_at) DESC
		LIMIT ?`, term, limit)
	if err != nil {
		return nil, fmt.Errorf("search environment keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []EnvKeyHit{}
	for rows.Next() {
		var hit EnvKeyHit
		var minRedacted int64
		if err := rows.Scan(&hit.Key, &hit.ProjectID, &hit.ProjectName, &hit.Service,
			&hit.LastSeenAt, &minRedacted); err != nil {
			return nil, fmt.Errorf("scan environment key hit: %w", err)
		}
		// MIN is 0 when any observation was kept readable.
		hit.Readable = minRedacted == 0
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (s *Store) searchFiles(ctx context.Context, term string, limit int) ([]FileHit, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT cf.path, s.project_id, p.name, MAX(s.taken_at)
		FROM compose_files cf
		JOIN snapshots s ON s.id = cf.snapshot_id
		JOIN projects p ON p.id = s.project_id
		WHERE instr(lower(cf.path), ?) > 0
		GROUP BY cf.path, s.project_id, p.name
		ORDER BY MAX(s.taken_at) DESC
		LIMIT ?`, term, limit)
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []FileHit{}
	for rows.Next() {
		var hit FileHit
		if err := rows.Scan(&hit.Path, &hit.ProjectID, &hit.ProjectName, &hit.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan file hit: %w", err)
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (s *Store) searchEvents(ctx context.Context, term string, limit int) ([]EventHit, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT id, project_id, service, ts, source, type, severity, message
		FROM events
		WHERE instr(lower(type), ?) > 0
		   OR instr(lower(message), ?) > 0
		   OR instr(lower(service), ?) > 0
		ORDER BY ts DESC
		LIMIT ?`, term, term, term, limit)
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []EventHit{}
	for rows.Next() {
		var hit EventHit
		if err := rows.Scan(&hit.ID, &hit.ProjectID, &hit.Service, &hit.TS,
			&hit.Source, &hit.Type, &hit.Severity, &hit.Message); err != nil {
			return nil, fmt.Errorf("scan event hit: %w", err)
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}
