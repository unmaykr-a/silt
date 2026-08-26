// Package textdiff computes line-level differences between two texts.
//
// Hand-written rather than pulled in: an LCS line diff is a well-understood
// algorithm, this is a core feature rather than an incidental one, and the
// brief asks that every dependency earn its place.
package textdiff

import "strings"

// Op is what happened to a line.
type Op string

const (
	OpEqual  Op = "equal"
	OpInsert Op = "insert"
	OpDelete Op = "delete"
)

// Line is one line of a diff.
//
// Both line numbers are carried because a reader needs to locate the line in
// whichever version they are looking at, and only one of the two exists for an
// inserted or deleted line.
type Line struct {
	Op Op `json:"op"`
	// OldNumber is the 1-based line number in the "before" text, 0 if absent.
	OldNumber int `json:"old_number"`
	// NewNumber is the 1-based line number in the "after" text, 0 if absent.
	NewNumber int    `json:"new_number"`
	Text      string `json:"text"`
}

// Hunk is a run of changed lines with surrounding context.
type Hunk struct {
	OldStart int    `json:"old_start"`
	OldCount int    `json:"old_count"`
	NewStart int    `json:"new_start"`
	NewCount int    `json:"new_count"`
	Lines    []Line `json:"lines"`
}

// Result is a complete comparison.
type Result struct {
	Hunks     []Hunk `json:"hunks"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Identical bool   `json:"identical"`
}

// DefaultContext is how many unchanged lines surround each hunk.
const DefaultContext = 3

// Lines splits a text the way a diff should see it.
//
// A trailing newline produces one empty final element in a naive split, which
// would show as a phantom changed line whenever a file gains or loses its
// final newline. Drop it, but only the one.
func Lines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// Compute diffs two texts with the default amount of context.
func Compute(before, after string) Result {
	return ComputeWithContext(before, after, DefaultContext)
}

// ComputeWithContext diffs two texts. A negative context returns every line.
func ComputeWithContext(before, after string, context int) Result {
	oldLines := Lines(before)
	newLines := Lines(after)

	script := lcsScript(oldLines, newLines)

	result := Result{Hunks: []Hunk{}}
	for _, line := range script {
		switch line.Op {
		case OpInsert:
			result.Added++
		case OpDelete:
			result.Removed++
		}
	}
	result.Identical = result.Added == 0 && result.Removed == 0

	if context < 0 {
		if len(script) > 0 {
			result.Hunks = []Hunk{wholeHunk(script)}
		}
		return result
	}
	result.Hunks = group(script, context)
	return result
}

// lcsScript produces the full edit script via a longest-common-subsequence
// table.
//
// The table is O(n*m); for compose files, which run to hundreds of lines, that
// is nothing. A file large enough to matter is refused by the size cap long
// before it reaches here.
func lcsScript(oldLines, newLines []string) []Line {
	n, m := len(oldLines), len(newLines)

	// lengths[i][j] is the LCS length of oldLines[i:] and newLines[j:].
	lengths := make([][]int, n+1)
	for i := range lengths {
		lengths[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lengths[i][j] = lengths[i+1][j+1] + 1
				continue
			}
			if lengths[i+1][j] >= lengths[i][j+1] {
				lengths[i][j] = lengths[i+1][j]
			} else {
				lengths[i][j] = lengths[i][j+1]
			}
		}
	}

	out := make([]Line, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			out = append(out, Line{Op: OpEqual, OldNumber: i + 1, NewNumber: j + 1, Text: oldLines[i]})
			i++
			j++
		case lengths[i+1][j] >= lengths[i][j+1]:
			out = append(out, Line{Op: OpDelete, OldNumber: i + 1, Text: oldLines[i]})
			i++
		default:
			out = append(out, Line{Op: OpInsert, NewNumber: j + 1, Text: newLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, Line{Op: OpDelete, OldNumber: i + 1, Text: oldLines[i]})
	}
	for ; j < m; j++ {
		out = append(out, Line{Op: OpInsert, NewNumber: j + 1, Text: newLines[j]})
	}
	return out
}

// group collects changed lines into hunks with `context` unchanged lines
// either side, merging hunks that would otherwise overlap.
func group(script []Line, context int) []Hunk {
	changed := make([]int, 0)
	for i, line := range script {
		if line.Op != OpEqual {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return []Hunk{}
	}

	hunks := []Hunk{}
	start := max(0, changed[0]-context)
	end := min(len(script), changed[0]+context+1)

	for _, index := range changed[1:] {
		if index-context <= end {
			end = min(len(script), index+context+1)
			continue
		}
		hunks = append(hunks, buildHunk(script[start:end]))
		start = max(0, index-context)
		end = min(len(script), index+context+1)
	}
	hunks = append(hunks, buildHunk(script[start:end]))
	return hunks
}

func buildHunk(lines []Line) Hunk {
	h := Hunk{Lines: lines}
	for _, line := range lines {
		if line.OldNumber > 0 {
			if h.OldStart == 0 {
				h.OldStart = line.OldNumber
			}
			h.OldCount++
		}
		if line.NewNumber > 0 {
			if h.NewStart == 0 {
				h.NewStart = line.NewNumber
			}
			h.NewCount++
		}
	}
	return h
}

func wholeHunk(script []Line) Hunk { return buildHunk(script) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
