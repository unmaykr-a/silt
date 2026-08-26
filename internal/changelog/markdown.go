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
		if r.Summary != "" {
			fmt.Fprintf(&b, "\n%s\n", r.Summary)
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
			fmt.Fprintf(&b, "\n### %s\n\n", title(kind))
			for _, line := range lines {
				fmt.Fprintf(&b, "- %s\n", line)
			}
		}
	}
	return b.String()
}

func title(k Kind) string {
	s := string(k)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
