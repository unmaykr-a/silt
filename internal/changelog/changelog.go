// Package changelog is Silt's release history.
//
// The history lives here as Go data rather than as a markdown file parsed at
// runtime, for two reasons. The UI wants it structured — a release, a date, a
// set of entries grouped by kind — and parsing prose to get that back is a
// guessing game. And the root CHANGELOG.md is generated from this, with a test
// that fails when the two drift, so there is one source of truth rather than
// two that agree until someone edits the wrong one.
package changelog

// Kind groups the entries within a release.
type Kind string

const (
	Added    Kind = "added"
	Changed  Kind = "changed"
	Fixed    Kind = "fixed"
	Removed  Kind = "removed"
	Security Kind = "security"
)

// Order is the order kinds are rendered in.
var Order = []Kind{Added, Changed, Fixed, Security, Removed}

// Entry is one line of a release.
type Entry struct {
	Kind Kind   `json:"kind"`
	Text string `json:"text"`
}

// Release is one published version.
type Release struct {
	Version string  `json:"version"`
	Date    string  `json:"date"`
	Summary string  `json:"summary,omitempty"`
	Entries []Entry `json:"entries"`
}

// Releases is the history, newest first.
var Releases = []Release{
	{
		Version: "0.3.0",
		Date:    "2026-08-26",
		Summary: "Compose files you can read, a timeline you can scan, and a layout you can choose.",
		Entries: []Entry{
			{Added, "Syntax highlighting for compose files, everywhere they are shown. Keys, strings, numbers, comments, anchors and ${VAR} references each get their own colour, and Silt's own redaction placeholder gets one too so a hidden value is visible as hidden."},
			{Added, "Comparing two snapshots as YAML now marks what actually changed: changed lines are tinted, the changed words inside them are highlighted, and the unchanged runs between them collapse. Side-by-side or unified, with the context adjustable."},
			{Added, "A second navigation layout. Sections can sit across the top or stacked in a left rail above the project list, the way authentik and Dockhand do it."},
			{Added, "Date and time preferences: 24-hour or 12-hour, day/month/year order, relative or absolute timestamps, and optional seconds. They live in your browser, so two people looking at the same Silt each get their own."},
			{Added, "A Ko-fi link in the version dialog, and a funding entry on the repository."},
			{Changed, "The project list scrolls on its own instead of making the page taller. A forty-project rail no longer decides how far the timeline scrolls."},
			{Changed, "The timeline reads at a glance: a fixed time column, a severity bar instead of a dot, day headings, hairline separators instead of a box per row, and bursts that expand in place. The density strip is taller by default, resizable, has a readable axis, and its legend doubles as a series filter."},
			{Changed, "Settings are organised into sections with their own navigation rather than one page taller than the window."},
			{Changed, "The strata-and-marker mark is now used throughout — header, favicon, sign-in, empty states."},
			{Fixed, "Choosing \"whole file\" when comparing YAML could lock the tab: the context window was walked one step at a time up to the maximum safe integer."},
		},
	},
	{
		Version: "0.2.0",
		Date:    "2026-08-26",
		Summary: "Settings you can edit, a timeline you can grab, and somewhere to put the version number.",
		Entries: []Entry{
			{Added, "Settings are editable from the UI. The environment stays the baseline; edits are stored as overrides on top of it, and every field shows whether it is coming from the environment or from here."},
			{Added, "Changed settings take effect without a restart: the snapshot interval, retention windows, keep-list, notification targets, base URL, log level and ingest token are all re-read by the running collector."},
			{Added, "The density strip is interactive — hover for the counts in a bucket, drag to zoom into a window, double-click to zoom back out."},
			{Added, "A version button in the header that opens the changelog."},
			{Changed, "Navigation is a top bar with Timeline, Projects and Settings, plus a project sidebar. Settings no longer sits at the bottom of a thirty-item scroll."},
			{Added, "A Projects page that lists every stack with its last change, for installs where the sidebar is a scroll rather than a glance."},
			{Changed, "The theme toggle is an animated sun/moon rather than the word \"Light\"."},
			{Security, "Notification targets are masked when read back. A shoutrrr URL carries the credential for the service it points at, so the settings screen shows the scheme and, where it is a real host, the host — never the token."},
		},
	},
	{
		Version: "0.1.0",
		Date:    "2026-08-25",
		Summary: "First working Silt: it watches, it records, it shows you what changed.",
		Entries: []Entry{
			{Added, "Docker event stream and interval reconcile, recording a snapshot of every Compose project whenever its configuration changes."},
			{Added, "Keep-list redaction: every environment value is a keyed digest unless its key is on the safe list, so the history answers \"when did this change?\" without ever holding the secret."},
			{Added, "Compose file capture with per-line diffs, and manual marking of lines to hide — click a line to redact it, click it again to reveal it."},
			{Added, "Timeline, project, service, diff and settings screens, live-updated over SSE."},
			{Added, "Retention with separate windows for changed snapshots, runtime-only snapshots and events, plus optional vacuuming."},
			{Added, "Notifications through shoutrrr, filtered by change kind and severity."},
			{Added, "Authentication: forward-auth from a reverse proxy, with a bcrypt password fallback."},
			{Added, "Multi-architecture images at ghcr.io/unmaykr-a/silt."},
		},
	},
}

// Current is the newest released version.
func Current() string {
	if len(Releases) == 0 {
		return "0.0.0"
	}
	return Releases[0].Version
}
