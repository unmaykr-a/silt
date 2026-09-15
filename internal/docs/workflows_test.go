package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Workflows are parsed here because GitHub is the only other thing that parses
// them, and it does so after a push — so an invalid one is a red X on a commit
// that is already public.
//
// This exists because the wiki workflow was written with a multi-line commit
// message inside a `run: |` block. An unindented continuation line ends a YAML
// block scalar, so the file was a parse error that looked entirely reasonable
// and would have failed only on GitHub.

func TestEveryWorkflowIsValidYAML(t *testing.T) {
	paths, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 3 {
		t.Fatalf("found %d workflows; the glob is wrong", len(paths))
	}

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Errorf("%s is not valid YAML: %v", filepath.Base(path), err)
			continue
		}
		// A workflow with no name renders as its filename in the Actions list,
		// and a workflow with no jobs is a file GitHub silently ignores.
		if _, ok := doc["name"]; !ok {
			t.Errorf("%s has no name", filepath.Base(path))
		}
		jobs, ok := doc["jobs"].(map[string]any)
		if !ok || len(jobs) == 0 {
			t.Errorf("%s declares no jobs", filepath.Base(path))
		}
	}
}

func TestTheWikiWorkflowPublishesEveryPage(t *testing.T) {
	// The workflow copies docs/wiki/*.md. If a page were ever added somewhere
	// else — a subdirectory, say — it would pass every other test here and
	// silently never reach the wiki.
	body, err := os.ReadFile("../../.github/workflows/wiki.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "docs/wiki/*.md") {
		t.Error("the wiki workflow no longer copies docs/wiki/*.md")
	}

	entries, err := os.ReadDir(wikiDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("docs/wiki/%s is a directory; the workflow copies *.md from the top level only",
				e.Name())
		}
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("docs/wiki/%s is not a .md file and will not be published", e.Name())
		}
	}
}
