package changelog_test

import (
	"os"
	"strings"
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

func TestNotesRenderOneRelease(t *testing.T) {
	current := changelog.Current()
	notes, ok := changelog.Notes(current)
	if !ok {
		t.Fatalf("no notes for the current release %s", current)
	}
	if notes == "" {
		t.Fatal("notes are empty")
	}
	// The heading belongs to the changelog file, not to a release page that
	// already carries the version in its title.
	if strings.HasPrefix(notes, "#") || strings.Contains(notes, "## "+current) {
		t.Errorf("notes repeat the release heading:\n%s", notes)
	}
	if strings.HasPrefix(notes, "\n") {
		t.Error("notes begin with a blank line")
	}
	// And they must be the same text the changelog carries, or a published
	// release and the file disagree by the second one.
	if !strings.Contains(changelog.Markdown(), strings.TrimSpace(notes)) {
		t.Error("notes are not a substring of the generated changelog")
	}
}

func TestNotesAcceptATagOrAVersion(t *testing.T) {
	current := changelog.Current()
	plain, _ := changelog.Notes(current)
	tagged, ok := changelog.Notes("v" + current)
	if !ok || tagged != plain {
		t.Error("a v-prefixed tag does not resolve to the same release")
	}
	if _, ok := changelog.Notes("0.0.0-nope"); ok {
		t.Error("an unknown version reported success")
	}
}
