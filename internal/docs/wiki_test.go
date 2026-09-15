package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The wiki's shape.
//
// A wiki rots in two specific ways, both invisible to anyone who wrote it: a
// page stops being linked from the sidebar and nobody finds it again, and a
// link points at a page that was renamed. Neither breaks a build and neither
// shows up in review, because the reviewer already knows where everything is.

const wikiDir = "../../docs/wiki"

// pages returns the wiki page names, without the special sidebar and footer.
func pages(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(wikiDir)
	if err != nil {
		t.Fatalf("read %s: %v", wikiDir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, "_") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".md"))
	}
	if len(out) < 10 {
		t.Fatalf("found %d wiki pages; the directory or the filter is wrong", len(out))
	}
	sort.Strings(out)
	return out
}

func read(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(wikiDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// wikiLink matches a markdown link to a wiki page: not http, not an anchor on
// this page, not an image.
var wikiLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// internalTargets returns the wiki page names one page links to.
func internalTargets(body string) []string {
	var out []string
	for _, m := range wikiLink.FindAllStringSubmatch(body, -1) {
		target := m[1]
		switch {
		case strings.HasPrefix(target, "http"), strings.HasPrefix(target, "#"),
			strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "screenshots/"):
			continue
		}
		// A link may carry an anchor: Backups#automating-it.
		if i := strings.Index(target, "#"); i >= 0 {
			target = target[:i]
		}
		if target == "" {
			continue
		}
		out = append(out, target)
	}
	return out
}

func TestEveryPageIsInTheSidebar(t *testing.T) {
	// A page nothing links to is a page nobody reads. GitHub's wiki has no
	// automatic index beyond an alphabetical list most people never open.
	sidebar := read(t, "_Sidebar.md")
	var missing []string
	for _, page := range pages(t) {
		if !strings.Contains(sidebar, "("+page+")") {
			missing = append(missing, page)
		}
	}
	if len(missing) > 0 {
		t.Errorf("_Sidebar.md does not link %d page(s): %s", len(missing), strings.Join(missing, ", "))
	}
}

func TestEveryInternalLinkResolves(t *testing.T) {
	// GitHub renders a link to a missing wiki page as a live link that 404s, so
	// a renamed page leaves working-looking links all over the wiki.
	known := map[string]bool{}
	for _, page := range pages(t) {
		known[page] = true
	}

	entries, err := os.ReadDir(wikiDir)
	if err != nil {
		t.Fatal(err)
	}
	var broken []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		for _, target := range internalTargets(read(t, e.Name())) {
			if !known[target] {
				broken = append(broken, e.Name()+" → "+target)
			}
		}
	}
	sort.Strings(broken)
	if len(broken) > 0 {
		t.Errorf("links to pages that do not exist:\n  %s", strings.Join(broken, "\n  "))
	}
}

func TestTheSidebarLinksNothingMissing(t *testing.T) {
	// The other direction: a sidebar entry for a page that was deleted.
	known := map[string]bool{}
	for _, page := range pages(t) {
		known[page] = true
	}
	known["Home"] = true

	var broken []string
	for _, target := range internalTargets(read(t, "_Sidebar.md")) {
		if !known[target] {
			broken = append(broken, target)
		}
	}
	if len(broken) > 0 {
		t.Errorf("_Sidebar.md links pages that do not exist: %s", strings.Join(broken, ", "))
	}
}

func TestEveryPageIsReachableFromHome(t *testing.T) {
	// Home is the landing page, and someone who arrives there should be able to
	// get anywhere without the sidebar — which is not rendered on narrow
	// screens, and is not there at all in a search result.
	home := read(t, "Home.md")
	var missing []string
	for _, page := range pages(t) {
		if page == "Home" {
			continue
		}
		if !strings.Contains(home, "("+page+")") {
			missing = append(missing, page)
		}
	}
	if len(missing) > 0 {
		t.Errorf("Home.md does not link %d page(s): %s", len(missing), strings.Join(missing, ", "))
	}
}

func TestNoPageIsAStub(t *testing.T) {
	// A wiki's failure mode is a page that exists, is linked, and says nothing.
	for _, page := range pages(t) {
		body := read(t, page+".md")
		if len(body) < 400 {
			t.Errorf("%s.md is %d bytes; that is a stub, not a page", page, len(body))
		}
		if !strings.HasPrefix(strings.TrimSpace(body), "# ") {
			t.Errorf("%s.md does not start with a heading", page)
		}
	}
}

func TestNoTrailingWhitespaceOrTabs(t *testing.T) {
	// GitHub renders a line ending in two spaces as a hard break, which is a
	// surprising diff to leave behind by accident.
	entries, err := os.ReadDir(wikiDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		for i, line := range strings.Split(read(t, e.Name()), "\n") {
			if strings.HasSuffix(line, " ") || strings.Contains(line, "\t") {
				t.Errorf("%s:%d has trailing whitespace or a tab", e.Name(), i+1)
			}
		}
	}
}
