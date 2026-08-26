package textdiff_test

import (
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/textdiff"
)

func flatten(r textdiff.Result) []string {
	var out []string
	for _, h := range r.Hunks {
		for _, l := range h.Lines {
			switch l.Op {
			case textdiff.OpInsert:
				out = append(out, "+"+l.Text)
			case textdiff.OpDelete:
				out = append(out, "-"+l.Text)
			default:
				out = append(out, " "+l.Text)
			}
		}
	}
	return out
}

func TestIdenticalTextHasNoHunks(t *testing.T) {
	text := "services:\n  radarr:\n    image: radarr:latest\n"
	r := textdiff.Compute(text, text)
	if !r.Identical || len(r.Hunks) != 0 {
		t.Errorf("identical texts produced %d hunks (identical=%v)", len(r.Hunks), r.Identical)
	}
}

// The change people actually make: one line edited in place.
func TestSingleLineChange(t *testing.T) {
	before := "services:\n  radarr:\n    image: radarr:4.0\n    restart: always\n"
	after := "services:\n  radarr:\n    image: radarr:5.0\n    restart: always\n"

	r := textdiff.Compute(before, after)
	if r.Added != 1 || r.Removed != 1 {
		t.Fatalf("added=%d removed=%d, want 1 and 1", r.Added, r.Removed)
	}
	got := flatten(r)
	if !contains(got, "-    image: radarr:4.0") || !contains(got, "+    image: radarr:5.0") {
		t.Errorf("diff did not show the edited line: %v", got)
	}
	// The line numbers must locate the change in each version.
	for _, h := range r.Hunks {
		for _, l := range h.Lines {
			if l.Op == textdiff.OpDelete && l.OldNumber != 3 {
				t.Errorf("deleted line reported old number %d, want 3", l.OldNumber)
			}
			if l.Op == textdiff.OpInsert && l.NewNumber != 3 {
				t.Errorf("inserted line reported new number %d, want 3", l.NewNumber)
			}
		}
	}
}

func TestPureInsertionAndDeletion(t *testing.T) {
	base := "a\nb\nc\n"

	inserted := textdiff.Compute(base, "a\nb\nNEW\nc\n")
	if inserted.Added != 1 || inserted.Removed != 0 {
		t.Errorf("insertion: added=%d removed=%d", inserted.Added, inserted.Removed)
	}

	deleted := textdiff.Compute(base, "a\nc\n")
	if deleted.Added != 0 || deleted.Removed != 1 {
		t.Errorf("deletion: added=%d removed=%d", deleted.Added, deleted.Removed)
	}
}

func TestEmptySides(t *testing.T) {
	created := textdiff.Compute("", "a\nb\n")
	if created.Added != 2 || created.Removed != 0 {
		t.Errorf("new file: added=%d removed=%d, want 2 and 0", created.Added, created.Removed)
	}
	removed := textdiff.Compute("a\nb\n", "")
	if removed.Added != 0 || removed.Removed != 2 {
		t.Errorf("deleted file: added=%d removed=%d, want 0 and 2", removed.Added, removed.Removed)
	}
}

// A file gaining or losing its trailing newline must not read as a changed
// final line.
func TestTrailingNewlineIsNotAChange(t *testing.T) {
	if r := textdiff.Compute("a\nb\n", "a\nb"); !r.Identical {
		t.Errorf("trailing newline reported as a change: %v", flatten(r))
	}
	if r := textdiff.Compute("a\nb\r\n", "a\nb\n"); !r.Identical {
		t.Errorf("CRLF reported as a change: %v", flatten(r))
	}
}

// Distant changes belong in separate hunks; nearby ones should merge, or the
// reader sees the same context twice.
func TestHunkGrouping(t *testing.T) {
	before := strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
		"11", "12", "13", "14", "15", "16", "17", "18", "19", "20"}, "\n")
	after := strings.Join([]string{"1", "CHANGED", "3", "4", "5", "6", "7", "8", "9", "10",
		"11", "12", "13", "14", "15", "16", "17", "18", "19", "TWENTY"}, "\n")

	r := textdiff.Compute(before, after)
	if len(r.Hunks) != 2 {
		t.Errorf("got %d hunks for two distant changes, want 2", len(r.Hunks))
	}

	adjacent := textdiff.Compute(before, strings.Join([]string{"1", "X", "Y", "4", "5", "6", "7", "8", "9", "10",
		"11", "12", "13", "14", "15", "16", "17", "18", "19", "20"}, "\n"))
	if len(adjacent.Hunks) != 1 {
		t.Errorf("got %d hunks for adjacent changes, want them merged into 1", len(adjacent.Hunks))
	}
}

func TestContextLinesSurroundChanges(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n"
	after := "1\n2\n3\n4\nCHANGED\n6\n7\n8\n9\n"

	r := textdiff.ComputeWithContext(before, after, 2)
	if len(r.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(r.Hunks))
	}
	// Two lines of context either side, plus the delete and the insert.
	if got := len(r.Hunks[0].Lines); got != 6 {
		t.Errorf("hunk has %d lines, want 6 (2 context + delete + insert + 2 context): %v",
			got, flatten(r))
	}
}

func TestNegativeContextReturnsEverything(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	after := "1\n2\n3\n4\n5\n6\n7\n8\n9\nX\n"

	r := textdiff.ComputeWithContext(before, after, -1)
	if len(r.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(r.Hunks))
	}
	if got := len(r.Hunks[0].Lines); got != 11 {
		t.Errorf("full diff has %d lines, want 11 (9 equal + 1 delete + 1 insert)", got)
	}
}

// Reordering lines is a real change, unlike the set-valued fields elsewhere in
// Silt: a compose file's line order is what the reader is looking at.
func TestReorderIsAChange(t *testing.T) {
	r := textdiff.Compute("a\nb\n", "b\na\n")
	if r.Identical {
		t.Error("reordered lines reported as identical")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
