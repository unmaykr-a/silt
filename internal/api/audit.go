package api

import (
	"net/http"

	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/store"
)

// Recording what people did to Silt itself.
//
// Every write endpoint calls audit(). It is one line at the call site and
// never returns an error to the caller: refusing a settings change because its
// bookkeeping row could not be written would turn a logging failure into an
// outage. A failed write is logged and the action proceeds.

// audit records one administrative action, attributing it to whoever is asking.
func (s *Server) audit(r *http.Request, action string, detail map[string]any) {
	s.auditAs(r, action, false, detail)
}

// auditFailed records an action that was attempted and refused. A rejected
// sign-in is the row most worth keeping.
func (s *Server) auditFailed(r *http.Request, action string, detail map[string]any) {
	s.auditAs(r, action, true, detail)
}

func (s *Server) auditAs(r *http.Request, action string, failed bool, detail map[string]any) {
	actor, method := s.actor(r)
	err := s.store.RecordAudit(r.Context(), store.AuditRecord{
		Actor:  actor,
		Method: method,
		Action: action,
		Failed: failed,
		Detail: detail,
		Remote: clientKey(r),
	})
	if err != nil {
		s.log.Error("record audit entry", "action", action, "error", err)
	}
}

// actor names whoever is asking, and how they were identified.
//
// An install with no authentication configured has no actor to name, and
// inventing one would be worse than an empty string: "admin" would read as a
// real account on a Silt where anybody who can reach the port is admin.
func (s *Server) actor(r *http.Request) (string, string) {
	if s.gate == nil || !s.gate.Enabled() {
		return "", store.AuditBySystem
	}
	id, ok := s.identify(r)
	if !ok {
		return "", store.AuditBySystem
	}
	name := id.Name
	if name == "" {
		name = id.Subject
	}
	switch id.Method {
	case auth.MethodOIDC:
		return name, store.AuditByOIDC
	case auth.MethodProxy:
		return name, store.AuditByProxy
	case auth.MethodPassword:
		return name, store.AuditByLocal
	default:
		return name, store.AuditBySystem
	}
}

type auditEntryResponse struct {
	ID     int64          `json:"id"`
	TS     int64          `json:"ts"`
	Actor  string         `json:"actor,omitempty"`
	Method string         `json:"method,omitempty"`
	Action string         `json:"action"`
	OK     bool           `json:"ok"`
	Detail map[string]any `json:"detail,omitempty"`
	Remote string         `json:"remote,omitempty"`
}

type auditResponse struct {
	Entries []auditEntryResponse `json:"entries"`
	Total   int64                `json:"total"`
}

// listAudit serves GET /api/audit.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListAudit(r.Context(), queryInt(r, "before", 0), queryLimit(r, 100, 500))
	if err != nil {
		s.log.Error("read audit log", "error", err)
		writeError(w, http.StatusInternalServerError, "read audit log")
		return
	}
	total, err := s.store.CountAudit(r.Context())
	if err != nil {
		s.log.Error("count audit log", "error", err)
		writeError(w, http.StatusInternalServerError, "read audit log")
		return
	}

	out := auditResponse{Entries: make([]auditEntryResponse, 0, len(entries)), Total: total}
	for _, e := range entries {
		out.Entries = append(out.Entries, auditEntryResponse{
			ID:     e.ID,
			TS:     e.TS,
			Actor:  e.Actor,
			Method: e.Method,
			Action: e.Action,
			OK:     e.OK,
			Detail: e.Detail,
			Remote: e.Remote,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
