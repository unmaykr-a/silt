package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// Everything in a window, as a file.
//
// The ask was for something a person can read and something that can be handed
// to a model to diagnose, which sounds like two formats and is mostly one.
// Markdown is the default because it satisfies both: headings and a table are
// skimmable, and an LLM reads the structure rather than guessing at columns.
// JSON is there for anything that is actually parsing it, where Markdown would
// be a worse CSV.
//
// What makes it diagnosable rather than merely complete is the summary. A
// thousand lines of chronology with no counts is a log file, and the first
// thing anyone does with a log file is count things. So the header does that
// first: what the window was, what was in it, which projects and which types,
// worst severity first.

const exportLimit = 5000

type exportEvent struct {
	Event   sqlcgen.Event
	Project string
}

func (s *Server) exportEvents(w http.ResponseWriter, r *http.Request) {
	to := queryInt(r, "to", 0)
	if to <= 0 {
		to = store.Now()
	}
	from := queryInt(r, "from", 0)
	if from <= 0 {
		from = to - (24 * time.Hour).Milliseconds()
	}
	if from > to {
		from, to = to, from
	}

	rows, err := s.store.RQ.ListEvents(r.Context(), sqlcgen.ListEventsParams{
		FromTs:    from,
		ToTs:      to,
		ProjectID: queryInt(r, "project", 0),
		Service:   r.URL.Query().Get("service"),
		Type:      r.URL.Query().Get("type"),
		Severity:  r.URL.Query().Get("severity"),
		MaxRows:   exportLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events")
		return
	}

	// Names once, not per row: the export is the one place where every event's
	// project is wanted and looking each one up would be a query per line.
	//
	// Per host, because ListProjects is scoped to one and a zero there matches
	// nothing rather than everything — which is how the first version of this
	// produced a Project column of dashes.
	names := map[int64]string{}
	if hosts, err := s.store.RQ.ListHosts(r.Context()); err == nil {
		for _, h := range hosts {
			projects, err := s.store.RQ.ListProjects(r.Context(), h.ID)
			if err != nil {
				continue
			}
			for _, p := range projects {
				names[p.ID] = p.Name
			}
		}
	}

	// Oldest first. The list endpoint is newest-first because a feed is read
	// from the top; a document is read from the beginning, and an incident
	// only makes sense forwards.
	events := make([]exportEvent, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		e := exportEvent{Event: rows[i]}
		if rows[i].ProjectID.Valid {
			e.Project = names[rows[i].ProjectID.Int64]
		}
		events = append(events, e)
	}

	host := s.conf().HostName
	stamp := time.UnixMilli(from).UTC().Format("20060102-1504")
	base := "silt-events-" + filenameSafe(host) + "-" + stamp

	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".json"))
		writeJSON(w, http.StatusOK, eventExportJSON(host, from, to, len(rows), events))
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".md"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(eventExportMarkdown(host, from, to, events, r.URL.Query())))
}

type exportJSONEvent struct {
	TS       string          `json:"ts"`
	TSMS     int64           `json:"ts_ms"`
	Type     string          `json:"type"`
	Severity string          `json:"severity"`
	Source   string          `json:"source"`
	Project  string          `json:"project,omitempty"`
	Service  string          `json:"service,omitempty"`
	Actor    string          `json:"actor,omitempty"`
	Message  string          `json:"message,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type exportJSON struct {
	Silt      string            `json:"silt"`
	Host      string            `json:"host"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	FromMS    int64             `json:"from_ms"`
	ToMS      int64             `json:"to_ms"`
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated"`
	Severity  map[string]int    `json:"by_severity"`
	Types     map[string]int    `json:"by_type"`
	Projects  map[string]int    `json:"by_project"`
	Events    []exportJSONEvent `json:"events"`
}

func eventExportJSON(host string, from, to int64, total int, events []exportEvent) exportJSON {
	out := exportJSON{
		Silt: "events", Host: host,
		From: stampOf(from), To: stampOf(to), FromMS: from, ToMS: to,
		Count:     len(events),
		Truncated: total >= exportLimit,
		Severity:  map[string]int{}, Types: map[string]int{}, Projects: map[string]int{},
		Events: make([]exportJSONEvent, 0, len(events)),
	}
	for _, e := range events {
		out.Severity[e.Event.Severity]++
		out.Types[e.Event.Type]++
		if e.Project != "" {
			out.Projects[e.Project]++
		}
		out.Events = append(out.Events, exportJSONEvent{
			TS: stampOf(e.Event.Ts), TSMS: e.Event.Ts,
			Type: e.Event.Type, Severity: e.Event.Severity, Source: e.Event.Source,
			Project: e.Project, Service: e.Event.Service,
			Actor: e.Event.Actor, Message: e.Event.Message,
			Payload: rawPayload(e.Event.Payload),
		})
	}
	return out
}

func eventExportMarkdown(host string, from, to int64, events []exportEvent, q map[string][]string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Silt events: %s\n\n", host)
	fmt.Fprintf(&b, "- **Window**: %s to %s (%s)\n", stampOf(from), stampOf(to), humanSpan(to-from))
	fmt.Fprintf(&b, "- **Events**: %d\n", len(events))
	if filters := describeFilters(q); filters != "" {
		fmt.Fprintf(&b, "- **Filters**: %s\n", filters)
	}
	if len(events) >= exportLimit {
		fmt.Fprintf(&b, "- **Truncated**: this is the first %d events in the window, not all of them. Narrow the range.\n", exportLimit)
	}
	b.WriteString("\nTimes are UTC. Container labels are not recorded on events; `withheld` counts how many the engine sent.\n")

	if len(events) == 0 {
		b.WriteString("\nNothing was recorded in this window.\n")
		return b.String()
	}

	// Counts before chronology. The first thing anyone does with a list of
	// events is count them, and a model asked to diagnose does the same.
	bySeverity := map[string]int{}
	byType := map[string]int{}
	byProject := map[string]int{}
	for _, e := range events {
		bySeverity[e.Event.Severity]++
		byType[e.Event.Type]++
		if e.Project != "" {
			byProject[e.Project]++
		}
	}

	b.WriteString("\n## Summary\n\n")
	b.WriteString("| Severity | Count |\n|---|---|\n")
	for _, sev := range []string{store.SeverityError, store.SeverityWarn, store.SeverityInfo} {
		if n := bySeverity[sev]; n > 0 {
			fmt.Fprintf(&b, "| %s | %d |\n", sev, n)
		}
	}

	b.WriteString("\n| Type | Count |\n|---|---|\n")
	for _, kv := range topCounts(byType, 15) {
		fmt.Fprintf(&b, "| `%s` | %d |\n", kv.key, kv.n)
	}

	if len(byProject) > 0 {
		b.WriteString("\n| Project | Count |\n|---|---|\n")
		for _, kv := range topCounts(byProject, 15) {
			fmt.Fprintf(&b, "| %s | %d |\n", kv.key, kv.n)
		}
	}

	// Errors and warnings on their own, because that is what someone opening
	// this is looking for and scrolling a thousand info lines to find them is
	// the problem the summary exists to solve.
	var notable []exportEvent
	for _, e := range events {
		if e.Event.Severity != store.SeverityInfo {
			notable = append(notable, e)
		}
	}
	if len(notable) > 0 {
		fmt.Fprintf(&b, "\n## Errors and warnings (%d)\n\n", len(notable))
		b.WriteString("| Time | Severity | Type | Project | Service | Detail |\n|---|---|---|---|---|---|\n")
		for _, e := range notable {
			fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s | %s |\n",
				stampOf(e.Event.Ts), e.Event.Severity, e.Event.Type,
				cell(e.Project), cell(e.Event.Service), cell(detailLine(e.Event)))
		}
	}

	b.WriteString("\n## Everything, in order\n\n")
	b.WriteString("| Time | Severity | Type | Project | Service | Detail |\n|---|---|---|---|---|---|\n")
	for _, e := range events {
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s | %s |\n",
			stampOf(e.Event.Ts), e.Event.Severity, e.Event.Type,
			cell(e.Project), cell(e.Event.Service), cell(detailLine(e.Event)))
	}
	return b.String()
}

// detailLine folds an event's message and its engine attributes into one cell.
//
// A column per attribute would be mostly empty: a die has an exit code, a kill
// has a signal, and almost nothing has both.
func detailLine(e sqlcgen.Event) string {
	parts := []string{}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	var payload map[string]any
	if e.Payload != "" {
		_ = json.Unmarshal([]byte(e.Payload), &payload)
	}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		switch k {
		// Already their own columns, or not a fact about the event.
		case "project", "action", "image":
			continue
		case "withheld_attributes":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, payload[k]))
	}
	if n, ok := payload["withheld_attributes"]; ok {
		parts = append(parts, fmt.Sprintf("withheld=%v", n))
	}
	return strings.Join(parts, ", ")
}

type countedKey struct {
	key string
	n   int
}

// topCounts orders by count then name, so two runs over the same data produce
// the same file and a diff between two exports means something.
func topCounts(in map[string]int, max int) []countedKey {
	out := make([]countedKey, 0, len(in))
	for k, n := range in {
		out = append(out, countedKey{k, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].key < out[j].key
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func describeFilters(q map[string][]string) string {
	var parts []string
	for _, name := range []string{"project", "service", "type", "severity"} {
		if v := first(q[name]); v != "" && v != "0" {
			parts = append(parts, name+"="+v)
		}
	}
	return strings.Join(parts, ", ")
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// cell makes a value safe for a Markdown table. A pipe in a message would end
// the column early and shift every one after it.
func cell(s string) string {
	if s == "" {
		return "-"
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func stampOf(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05")
}

// humanSpan is a window length in the units someone would say it in.
func humanSpan(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	case d >= 2*time.Hour:
		return fmt.Sprintf("%.0f hours", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	default:
		return d.String()
	}
}

func rawPayload(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}
