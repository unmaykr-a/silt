package store_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The typed query layer is generated from internal/store/queries by sqlc, and
// these guard the generation itself rather than any one query.

// TestQueryFilesAreASCII.
//
// sqlc truncates the end of a query by one byte for every multi-byte character
// in the comment above it. One em dash in a doc comment turned
//
//	ORDER BY p.id LIMIT 1
//
// into
//
//	ORDER BY p.id LIMIT
//
// which SQLite accepts as a syntax error at runtime, so every Docker event
// silently stopped finding its project. Nothing failed to compile and nothing
// said why.
//
// This project's comments are written with em dashes everywhere else, which is
// exactly why this needs to be a test rather than a note: the habit is correct
// in Go and wrong here, and the difference is invisible.
func TestQueryFilesAreASCII(t *testing.T) {
	for _, path := range queryFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, r := range string(raw) {
			if r > 127 {
				line := strings.Count(string(raw[:i]), "\n") + 1
				t.Errorf("%s:%d contains %q (%U). sqlc will silently cut %d byte(s) off the end of a query in this file; use ASCII here",
					filepath.Base(path), line, r, r, len(string(r))-1)
			}
		}
	}
}

// TestNoGeneratedQueryEndsMidStatement is the same failure caught from the
// other side, and catches it whatever the cause: a query whose last token is a
// bare keyword was cut off, not written that way.
func TestNoGeneratedQueryEndsMidStatement(t *testing.T) {
	dangling := regexp.MustCompile(`(?i)\b(LIMIT|OFFSET|ORDER BY|GROUP BY|AND|OR|WHERE|VALUES|SET|JOIN|ON|IN|NOT)\s*$`)
	constant := regexp.MustCompile("(?s)const (\\w+) = `(.*?)`")

	dir := filepath.Join("sqlcgen")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	checked := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range constant.FindAllStringSubmatch(string(raw), -1) {
			checked++
			if sql := strings.TrimSpace(m[2]); dangling.MatchString(sql) {
				lines := strings.Split(sql, "\n")
				t.Errorf("%s: query %s ends mid-statement with %q; it was truncated",
					e.Name(), m[1], lines[len(lines)-1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no generated queries were checked, so this test proves nothing")
	}
}

func queryFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	if err != nil {
		t.Fatalf("glob queries: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no query files found, so these tests prove nothing")
	}
	return paths
}
