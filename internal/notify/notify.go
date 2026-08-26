// Package notify sends alerts about configuration changes.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"
	"github.com/containrrr/shoutrrr/pkg/types"

	"github.com/unmaykr-a/silt/internal/diff"
)

// severityRank orders severities so a minimum threshold can be applied.
var severityRank = map[diff.Severity]int{
	diff.Low:    1,
	diff.Medium: 2,
	diff.High:   3,
}

// Filter decides which changes are worth interrupting someone for.
//
// Kinds and severity are ANDed: a change must be of a listed kind AND meet the
// minimum severity. Either alone lets through more than anyone wants — a
// homelab running Watchtower produces image changes constantly.
type Filter struct {
	Kinds       map[diff.Kind]bool
	MinSeverity diff.Severity
}

// ParseFilter builds a Filter from the configured kind list and threshold.
func ParseFilter(kinds []string, minSeverity string) (Filter, error) {
	f := Filter{Kinds: map[diff.Kind]bool{}}

	for _, k := range kinds {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if k == "all" || k == "*" {
			f.Kinds = nil // nil means every kind
			break
		}
		f.Kinds[diff.Kind(k)] = true
	}

	switch s := diff.Severity(strings.ToLower(strings.TrimSpace(minSeverity))); s {
	case diff.Low, diff.Medium, diff.High:
		f.MinSeverity = s
	case "":
		f.MinSeverity = diff.Medium
	default:
		return Filter{}, fmt.Errorf("SILT_NOTIFY_MIN_SEVERITY %q is not one of low, medium, high", minSeverity)
	}
	return f, nil
}

// Match returns the changes worth notifying about.
func (f Filter) Match(changes []diff.Change) []diff.Change {
	out := make([]diff.Change, 0, len(changes))
	for _, c := range changes {
		if f.Kinds != nil && !f.Kinds[c.Kind] {
			continue
		}
		if severityRank[c.Severity] < severityRank[f.MinSeverity] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Sender delivers notifications. Nil is a valid no-op sender, which is what an
// install with no configured URLs gets.
type Sender struct {
	router *router.ServiceRouter
	filter Filter
	log    *slog.Logger

	mu sync.Mutex
}

// New builds a Sender for the given shoutrrr URLs. An empty list returns nil,
// so callers can hold a nil *Sender and call Notify unconditionally.
func New(urls []string, filter Filter, log *slog.Logger) (*Sender, error) {
	clean := make([]string, 0, len(urls))
	for _, u := range urls {
		if u = strings.TrimSpace(u); u != "" {
			clean = append(clean, u)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}
	if log == nil {
		log = slog.Default()
	}

	r, err := shoutrrr.CreateSender(clean...)
	if err != nil {
		// Report the count, never the URLs: a shoutrrr URL carries the
		// credential for the service it targets.
		return nil, fmt.Errorf("configure %d notification target(s): %w", len(clean), err)
	}
	return &Sender{router: r, filter: filter, log: log}, nil
}

// Change is what a caller reports: a project whose configuration changed, and
// what changed in it.
type Change struct {
	Project    string
	SnapshotID int64
	FromID     int64
	Changes    []diff.Change
	BaseURL    string
}

// Notify sends one message if any change passes the filter.
func (s *Sender) Notify(ctx context.Context, c Change) {
	if s == nil {
		return
	}
	matched := s.filter.Match(c.Changes)
	if len(matched) == 0 {
		return
	}

	title, body := format(c, matched)

	// shoutrrr's sender is not documented as safe for concurrent use, and the
	// collector can snapshot several projects at once.
	s.mu.Lock()
	params := types.Params{"title": title}
	errs := s.router.Send(body, &params)
	s.mu.Unlock()

	for _, err := range errs {
		if err != nil {
			s.log.Error("notification failed", "error", err)
		}
	}
	if ctx.Err() == nil {
		s.log.Debug("notification sent", "project", c.Project, "changes", len(matched))
	}
}

// format renders a message. It groups by service so a stack-wide update reads
// as one event rather than a wall of individual lines.
func format(c Change, matched []diff.Change) (title, body string) {
	title = fmt.Sprintf("Silt: %s changed", c.Project)

	byService := map[string][]diff.Change{}
	for _, change := range matched {
		service := change.Service
		if service == "" {
			service = "project"
		}
		byService[service] = append(byService[service], change)
	}
	services := make([]string, 0, len(byService))
	for name := range byService {
		services = append(services, name)
	}
	sort.Strings(services)

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d change(s)\n", c.Project, len(matched))
	for _, service := range services {
		fmt.Fprintf(&b, "\n%s\n", service)
		for _, change := range byService[service] {
			fmt.Fprintf(&b, "  [%s] %s", change.Severity, change.Kind)
			if change.Before != "" || change.After != "" {
				fmt.Fprintf(&b, ": %s → %s", truncate(change.Before), truncate(change.After))
			}
			b.WriteString("\n")
		}
	}
	if c.BaseURL != "" && c.FromID > 0 {
		fmt.Fprintf(&b, "\n%s/diff?from=%d&to=%d\n", strings.TrimRight(c.BaseURL, "/"), c.FromID, c.SnapshotID)
	}
	return title, b.String()
}

// truncate keeps a digest-length value readable in a push notification.
func truncate(s string) string {
	const max = 40
	if len(s) <= max {
		if s == "" {
			return "(none)"
		}
		return s
	}
	return s[:max] + "…"
}
