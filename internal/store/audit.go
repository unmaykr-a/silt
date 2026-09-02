package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// The administrative trail: what people did to Silt itself.
//
// Silt's whole job is answering "what changed, and when" about a host. This
// answers the same question about Silt. On a single-operator install the
// answer is always "me", which is why it did not exist; the moment a second
// person can sign in it is the first question anyone asks, and it cannot be
// answered retroactively.

// Audit actions. Constants rather than free strings so the set is greppable
// and a typo does not create a category nobody will ever filter for.
const (
	AuditSignIn          = "auth.sign_in"
	AuditSignInFailed    = "auth.sign_in_failed"
	AuditSignOut         = "auth.sign_out"
	AuditPasswordChanged = "auth.password_changed"
	AuditAccountClaimed  = "auth.account_claimed"
	AuditAccountChanged  = "auth.account_changed"
	AuditSessionsRevoked = "auth.sessions_revoked"

	AuditSettingsChanged = "settings.changed"
	AuditSettingsReset   = "settings.reset"
	AuditNotifyTested    = "settings.notifications_tested"

	AuditPrune           = "maintenance.prune"
	AuditSnapshotForced  = "maintenance.snapshot"
	AuditRedactionAdded  = "redaction.rule_added"
	AuditRedactionRemove = "redaction.rule_removed"
)

// Identification methods, matching what the auth layer reports.
const (
	AuditByLocal  = "local"
	AuditByOIDC   = "oidc"
	AuditByProxy  = "proxy"
	AuditBySystem = "system"
)

// AuditEntry is one recorded action.
type AuditEntry struct {
	ID     int64
	TS     int64
	Actor  string
	Method string
	Action string
	OK     bool
	Detail map[string]any
	Remote string
}

// AuditRecord is what a caller reports.
type AuditRecord struct {
	// Actor is empty when Silt acted on its own — scheduled retention, say.
	Actor  string
	Method string
	Action string
	// Failed marks the action as attempted and refused. A failed sign-in is
	// the row most worth keeping, so it is recorded rather than dropped.
	Failed bool
	// Detail says what changed, never what it changed to. The settings screen
	// holds an ingest token and notification URLs; recording their values here
	// would put them in a table built to be read.
	Detail map[string]any
	Remote string
}

// RecordAudit writes one entry.
//
// It returns an error rather than swallowing one, but callers log and continue:
// refusing a settings change because its audit row could not be written would
// turn a bookkeeping failure into an outage.
func (s *Store) RecordAudit(ctx context.Context, r AuditRecord) error {
	detail := []byte("{}")
	if len(r.Detail) > 0 {
		encoded, err := json.Marshal(r.Detail)
		if err != nil {
			return fmt.Errorf("marshal audit detail: %w", err)
		}
		detail = encoded
	}
	ok := int64(1)
	if r.Failed {
		ok = 0
	}
	if r.Method == "" {
		r.Method = AuditBySystem
	}
	return s.Q.InsertAudit(ctx, sqlcgen.InsertAuditParams{
		Ts:     Now(),
		Actor:  r.Actor,
		Method: r.Method,
		Action: r.Action,
		Ok:     ok,
		Detail: string(detail),
		Remote: r.Remote,
	})
}

// ListAudit reads the trail newest first.
func (s *Store) ListAudit(ctx context.Context, before int64, limit int64) ([]AuditEntry, error) {
	if before <= 0 {
		before = Now() + 1
	}
	rows, err := s.RQ.ListAudit(ctx, sqlcgen.ListAuditParams{Before: before, MaxRows: limit})
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, row := range rows {
		entry := AuditEntry{
			ID:     row.ID,
			TS:     row.Ts,
			Actor:  row.Actor,
			Method: row.Method,
			Action: row.Action,
			OK:     row.Ok != 0,
			Remote: row.Remote,
		}
		// A row whose detail will not parse is still a row worth showing: the
		// actor, action and time are the parts anyone came for.
		if row.Detail != "" {
			_ = json.Unmarshal([]byte(row.Detail), &entry.Detail)
		}
		out = append(out, entry)
	}
	return out, nil
}

// CountAudit reports how many entries exist, for the settings screen.
func (s *Store) CountAudit(ctx context.Context) (int64, error) {
	return s.RQ.CountAudit(ctx)
}

// PruneAudit deletes entries older than the cutoff and reports how many went.
func (s *Store) PruneAudit(ctx context.Context, cutoff int64) (int64, error) {
	return s.Q.PruneAudit(ctx, cutoff)
}
