package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// Event sources.
const (
	SourceDocker  = "docker"
	SourceSilt    = "silt"
	SourceWebhook = "webhook"
)

// Severities.
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// EventRecord is one event to persist.
type EventRecord struct {
	HostID    *int64
	ProjectID *int64
	Service   string
	TS        int64
	Source    string
	Type      string
	Severity  string
	Actor     string
	Message   string
	Payload   any
}

// RecordEvent persists one event and returns the stored row.
func (s *Store) RecordEvent(ctx context.Context, e EventRecord) (sqlcgen.Event, error) {
	if e.TS == 0 {
		e.TS = Now()
	}
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}

	payload := ""
	if e.Payload != nil {
		encoded, err := json.Marshal(e.Payload)
		if err != nil {
			return sqlcgen.Event{}, fmt.Errorf("marshal event payload: %w", err)
		}
		payload = string(encoded)
	}

	row, err := s.Q.InsertEvent(ctx, sqlcgen.InsertEventParams{
		HostID:    nullableID(e.HostID),
		ProjectID: nullableID(e.ProjectID),
		Service:   e.Service,
		Ts:        e.TS,
		Source:    e.Source,
		Type:      e.Type,
		Severity:  e.Severity,
		Actor:     e.Actor,
		Message:   e.Message,
		Payload:   payload,
	})
	if err != nil {
		return sqlcgen.Event{}, fmt.Errorf("insert event: %w", err)
	}
	return row, nil
}

func nullableID(id *int64) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}
