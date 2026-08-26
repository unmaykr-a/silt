package changelog_test

import (
	"os"
	"testing"

	"github.com/unmaykr-a/silt/internal/changelog"
)

// The markdown file is generated, so it can drift the moment someone edits the
// Go source without regenerating. This is the only thing that notices.
func TestMarkdownMatchesCheckedInFile(t *testing.T) {
	want := changelog.Markdown()
	got, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	if string(got) != want {
		t.Error("CHANGELOG.md is out of date; run `make changelog`")
	}
}

func TestReleasesAreOrderedAndComplete(t *testing.T) {
	if len(changelog.Releases) == 0 {
		t.Fatal("no releases")
	}
	seen := map[string]bool{}
	for _, r := range changelog.Releases {
		if r.Version == "" || r.Date == "" {
			t.Errorf("release %+v is missing a version or date", r)
		}
		if seen[r.Version] {
			t.Errorf("duplicate release %s", r.Version)
		}
		seen[r.Version] = true
		if len(r.Entries) == 0 {
			t.Errorf("release %s has no entries", r.Version)
		}
	}
	if changelog.Current() != changelog.Releases[0].Version {
		t.Error("Current does not report the newest release")
	}
}
