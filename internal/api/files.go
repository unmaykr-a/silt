package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
	"github.com/unmaykr-a/silt/internal/textdiff"
)

// FileReader is the capability the file endpoints need from the collector.
type FileReader interface {
	Enabled() bool
	Preview(path string, rules []compose.Rule) (compose.CapturedFile, error)
}

type composeFileResponse struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	LineCount int64  `json:"line_count"`
	Size      int64  `json:"size"`
	// ContentHash lets a caller tell which files differ between two snapshots
	// without fetching every file's text.
	ContentHash string `json:"content_hash,omitempty"`
}

func (s *Server) listSnapshotFiles(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.store.RQ.ListComposeFiles(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read compose files")
		return
	}
	out := make([]composeFileResponse, 0, len(rows))
	for _, f := range rows {
		out = append(out, composeFileResponse{
			Path: f.Path, Status: f.Status, LineCount: f.LineCount, Size: f.Size,
			ContentHash: f.ContentHash.String,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type fileContentResponse struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Content string `json:"content"`
}

func (s *Server) getSnapshotFile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	row, err := s.store.RQ.GetComposeFile(r.Context(), sqlcgen.GetComposeFileParams{SnapshotID: id, Path: path})
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "file not captured in this snapshot")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read compose file")
		return
	}

	content, err := s.store.ComposeFileContent(r.Context(), id, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file content")
		return
	}
	writeJSON(w, http.StatusOK, fileContentResponse{Path: path, Status: row.Status, Content: content})
}

type fileDiffResponse struct {
	Path   string          `json:"path"`
	From   Side            `json:"from"`
	To     Side            `json:"to"`
	Status string          `json:"status"`
	Diff   textdiff.Result `json:"diff"`
}

// Side identifies one snapshot in a file comparison.
type Side struct {
	SnapshotID int64 `json:"id"`
	TakenAt    int64 `json:"taken_at"`
}

// getFileDiff compares one file between two snapshots, line by line.
//
// This is the view that answers "what line exactly changed", which the
// structured diff cannot: it reports that an environment key moved, not where
// in the file it lives or what sits around it.
func (s *Server) getFileDiff(w http.ResponseWriter, r *http.Request) {
	fromID := queryInt(r, "from", 0)
	toID := queryInt(r, "to", 0)
	path := r.URL.Query().Get("path")
	if fromID <= 0 || toID <= 0 || path == "" {
		writeError(w, http.StatusBadRequest, "from, to and path are required")
		return
	}

	fromSnap, err := s.store.RQ.GetSnapshot(r.Context(), fromID)
	if err != nil {
		writeSnapshotLoadError(w, err, fromID)
		return
	}
	toSnap, err := s.store.RQ.GetSnapshot(r.Context(), toID)
	if err != nil {
		writeSnapshotLoadError(w, err, toID)
		return
	}

	// A file absent from one side is a creation or deletion, not an error:
	// diffing against empty is exactly the right rendering.
	before, _ := s.store.ComposeFileContent(r.Context(), fromID, path)
	after, _ := s.store.ComposeFileContent(r.Context(), toID, path)

	context := int(queryInt(r, "context", int64(textdiff.DefaultContext)))
	if r.URL.Query().Get("context") == "full" {
		context = -1
	}

	writeJSON(w, http.StatusOK, fileDiffResponse{
		Path:   path,
		From:   Side{SnapshotID: fromSnap.ID, TakenAt: fromSnap.TakenAt},
		To:     Side{SnapshotID: toSnap.ID, TakenAt: toSnap.TakenAt},
		Status: "ok",
		Diff:   textdiff.ComputeWithContext(before, after, context),
	})
}

func (s *Server) listProjectFilePaths(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	paths, err := s.store.RQ.ProjectFilePaths(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file paths")
		return
	}
	if paths == nil {
		paths = []string{}
	}
	writeJSON(w, http.StatusOK, paths)
}

type previewResponse struct {
	Path   string        `json:"path"`
	Status string        `json:"status"`
	Lines  []redact.Line `json:"lines"`
}

// previewFile reads a file live and returns it redacted, storing nothing.
//
// The marking UI has to work from this rather than from a capture: a capture
// is already redacted, so there would be nothing left to decide about. Nothing
// here is persisted, and the redaction applied is exactly what a capture would
// apply — so what someone marks against is what would be written.
func (s *Server) previewFile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if s.files == nil || !s.files.Enabled() {
		writeError(w, http.StatusServiceUnavailable,
			"no compose roots are configured; mount them and set SILT_COMPOSE_ROOTS")
		return
	}
	if _, err := s.store.RQ.GetProject(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	// Only paths this project has actually referenced may be previewed, so the
	// endpoint cannot be used to read arbitrary files even within the roots.
	known, err := s.store.RQ.ProjectFilePaths(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file paths")
		return
	}
	if !containsString(known, path) {
		writeError(w, http.StatusNotFound, "this project has no file at that path")
		return
	}

	rules, err := s.store.RedactionRules(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read redaction rules")
		return
	}

	preview, err := s.files.Preview(path, rules)
	if err != nil {
		writeJSON(w, http.StatusOK, previewResponse{Path: path, Status: preview.Status, Lines: []redact.Line{}})
		return
	}
	writeJSON(w, http.StatusOK, previewResponse{Path: path, Status: preview.Status, Lines: preview.Lines})
}

type redactionRuleResponse struct {
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Key    string `json:"key,omitempty"`
	LineNo int64  `json:"line_no,omitempty"`
	Note   string `json:"note,omitempty"`
}

func (s *Server) listRedactionRules(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.store.RQ.ListRedactionRules(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read redaction rules")
		return
	}
	out := make([]redactionRuleResponse, 0, len(rows))
	for _, rule := range rows {
		out = append(out, redactionRuleResponse{
			ID: rule.ID, Path: rule.Path, Action: rule.Action,
			Kind: rule.Kind, Key: rule.Key, LineNo: rule.LineNo, Note: rule.Note,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type ruleRequest struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	LineNo int64  `json:"line_no"`
	Note   string `json:"note"`
}

func (s *Server) postRedactionRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	var req ruleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}

	switch req.Action {
	case compose.ActionHide, compose.ActionReveal:
	default:
		writeError(w, http.StatusBadRequest, `action must be "hide" or "reveal"`)
		return
	}
	switch req.Kind {
	case compose.KindKey:
		if strings.TrimSpace(req.Key) == "" {
			writeError(w, http.StatusBadRequest, "key is required for a key rule")
			return
		}
		req.LineNo = 0
	case compose.KindLine:
		if req.LineNo <= 0 {
			writeError(w, http.StatusBadRequest, "line_no is required for a line rule")
			return
		}
		req.Key = ""
	default:
		writeError(w, http.StatusBadRequest, `kind must be "key" or "line"`)
		return
	}

	// A target cannot be both hidden and revealed, so adding one side clears
	// the other. Without this, toggling would accumulate contradictory rules
	// and the winner would depend on iteration order.
	if _, err := s.store.Q.DeleteOpposingRule(r.Context(), sqlcgen.DeleteOpposingRuleParams{
		ProjectID: id, Path: req.Path, Kind: req.Kind, Key: req.Key, LineNo: req.LineNo, Action: req.Action,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "replace opposing rule")
		return
	}

	rule, err := s.store.Q.InsertRedactionRule(r.Context(), sqlcgen.InsertRedactionRuleParams{
		ProjectID: id, Path: req.Path, Action: req.Action, Kind: req.Kind,
		Key: strings.TrimSpace(req.Key), LineNo: req.LineNo, Note: req.Note, CreatedAt: store.Now(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store redaction rule")
		return
	}
	writeJSON(w, http.StatusCreated, redactionRuleResponse{
		ID: rule.ID, Path: rule.Path, Action: rule.Action,
		Kind: rule.Kind, Key: rule.Key, LineNo: rule.LineNo, Note: rule.Note,
	})
}

func (s *Server) deleteRedactionRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ruleID, err := pathID(r, "rule")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.store.Q.DeleteRedactionRule(r.Context(), sqlcgen.DeleteRedactionRuleParams{ID: ruleID, ProjectID: id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete redaction rule")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
