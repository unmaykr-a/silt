package changelog

import (
	"fmt"
	"strings"
)

// header is the preamble of the generated CHANGELOG.md.
const header = `# Changelog

All notable changes to Silt are recorded here.

This file is generated from internal/changelog/changelog.go — edit that and run
` + "`make changelog`" + `.
`

// Markdown renders the history as the repository's CHANGELOG.md.
func Markdown() string {
	var b strings.Builder
	b.WriteString(header)
	for _, r := range Releases {
		fmt.Fprintf(&b, "\n## %s — %s\n", r.Version, r.Date)
		writeBody(&b, r)
	}
	return b.String()
}

// Notes renders one release as the body of a GitHub release.
//
// Same source as CHANGELOG.md, deliberately: a release whose notes were
// written separately is a release whose notes disagree with the changelog by
// the second one. The version is looked up rather than passed as an index so
// the tag drives it — `v0.14.0` and `0.14.0` both find the same entry.
func Notes(version string) (string, bool) {
	want := strings.TrimPrefix(strings.TrimSpace(version), "v")
	for _, r := range Releases {
		if r.Version != want {
			continue
		}
		var b strings.Builder
		writeBody(&b, r)
		return strings.TrimLeft(b.String(), "\n"), true
	}
	return "", false
}

func writeBody(b *strings.Builder, r Release) {
	if r.Summary != "" {
		fmt.Fprintf(b, "\n%s\n", r.Summary)
	}
	for _, kind := range Order {
		var lines []string
		for _, e := range r.Entries {
			if e.Kind == kind {
				lines = append(lines, e.Text)
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n### %s\n\n", title(kind))
		for _, line := range lines {
			fmt.Fprintf(b, "- %s\n", line)
		}
	}
}

func title(k Kind) string {
	s := string(k)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
