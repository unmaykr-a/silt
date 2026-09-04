package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/unmaykr-a/silt/internal/redact"
)

// Capture statuses for a compose file.
const (
	FileOK           = "ok"
	FileUnreadable   = "unreadable"
	FileOutsideRoots = "outside_roots"
	FileTooLarge     = "too_large"
)

// CapturedFile is one compose or .env file as captured for a snapshot.
type CapturedFile struct {
	Path      string
	Status    string
	Content   []byte // redacted; empty unless Status is FileOK
	Lines     []redact.Line
	LineCount int
	Size      int64
}

// Rule is one manual redaction decision.
type Rule struct {
	Path   string
	Action string // hide | reveal
	Kind   string // key | line
	Key    string
	LineNo int64
}

// Rule actions and kinds.
const (
	ActionHide   = "hide"
	ActionReveal = "reveal"
	KindKey      = "key"
	KindLine     = "line"
)

// RuleSet resolves rules for one file.
type RuleSet struct {
	byKey  map[string]string // key -> action
	byLine map[int]string    // line number -> action
}

// NewRuleSet selects the rules that apply to path and indexes them.
//
// A rule with an empty path applies to every file in the project, which is how
// someone hides a key like SMTP_PASSWORD everywhere at once.
func NewRuleSet(rules []Rule, path string) *RuleSet {
	rs := &RuleSet{byKey: map[string]string{}, byLine: map[int]string{}}
	for _, r := range rules {
		if r.Path != "" && r.Path != path {
			continue
		}
		switch r.Kind {
		case KindKey:
			if key := strings.TrimSpace(r.Key); key != "" {
				// A file-specific rule beats a project-wide one.
				if _, exists := rs.byKey[key]; !exists || r.Path != "" {
					rs.byKey[key] = r.Action
				}
			}
		case KindLine:
			if r.LineNo > 0 && r.Path == path {
				rs.byLine[int(r.LineNo)] = r.Action
			}
		}
	}
	return rs
}

// Decide implements redact.LinePolicy.
//
// A key rule is checked before a line rule: keys survive edits that move a
// line, so when both point at the same value the stable one should win.
func (rs *RuleSet) Decide(lineNo int, key string) (keep bool, matched bool) {
	if rs == nil {
		return false, false
	}
	if action, ok := rs.byKey[key]; ok {
		return action == ActionReveal, true
	}
	if action, ok := rs.byLine[lineNo]; ok {
		return action == ActionReveal, true
	}
	return false, false
}

// FileReader captures compose files from disk.
type FileReader struct {
	// Roots is an allowlist of directories a file may be read from.
	//
	// The paths come from container labels, and anyone who can start a
	// container can set those. Without this check a crafted label could point
	// Silt at any file the process can read.
	Roots []string
	// MaxBytes caps a single file.
	MaxBytes int64
	Redactor *redact.Redactor

	// resolved is Roots plus each root's symlink-resolved form, computed once.
	once     sync.Once
	resolved []string
}

// Enabled reports whether any root is configured.
func (f *FileReader) Enabled() bool { return f != nil && len(f.Roots) > 0 }

// Capture reads and redacts the given paths.
func (f *FileReader) Capture(paths []string, rules []Rule) []CapturedFile {
	out := make([]CapturedFile, 0, len(paths))
	for _, path := range paths {
		out = append(out, f.captureOne(path, rules))
	}
	return out
}

func (f *FileReader) captureOne(path string, rules []Rule) CapturedFile {
	captured := CapturedFile{Path: path, Status: FileOutsideRoots}
	if !f.Enabled() {
		captured.Status = FileUnreadable
		return captured
	}

	resolved, err := f.resolve(path)
	if err != nil {
		// Distinguish "not allowed" from "not there": the first is a
		// configuration problem the operator can fix, the second is normal
		// when compose roots are not mounted.
		if os.IsNotExist(err) {
			captured.Status = FileUnreadable
		}
		return captured
	}

	info, err := os.Stat(resolved)
	if err != nil {
		captured.Status = FileUnreadable
		return captured
	}
	captured.Size = info.Size()
	if info.Size() > f.MaxBytes {
		captured.Status = FileTooLarge
		return captured
	}

	raw, err := os.ReadFile(resolved)
	if err != nil {
		captured.Status = FileUnreadable
		return captured
	}

	content, lines := f.Redactor.ComposeText(raw, NewRuleSet(rules, path))
	captured.Status = FileOK
	captured.Content = content
	captured.Lines = lines
	captured.LineCount = len(lines)
	return captured
}

// Preview reads one file live and returns it redacted, without storing
// anything.
//
// This is what the marking UI works from. Showing a stored capture would be
// circular: the capture is already redacted, so there would be nothing left to
// decide about.
func (f *FileReader) Preview(path string, rules []Rule) (CapturedFile, error) {
	if !f.Enabled() {
		return CapturedFile{}, fmt.Errorf("no compose roots are configured; set SILT_COMPOSE_ROOTS")
	}
	captured := f.captureOne(path, rules)
	switch captured.Status {
	case FileOK:
		return captured, nil
	case FileOutsideRoots:
		return captured, fmt.Errorf("%s is not under any configured compose root", path)
	case FileTooLarge:
		return captured, fmt.Errorf("%s is larger than the configured limit", path)
	default:
		return captured, fmt.Errorf("%s could not be read", path)
	}
}

// resolve validates that path is under a configured root, following symlinks
// before deciding.
//
// Checking the literal path would be defeated by a symlink inside a mounted
// root pointing anywhere on the filesystem.
func (f *FileReader) resolve(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}
	clean := filepath.Clean(path)
	if !f.underRoot(clean) {
		return "", fmt.Errorf("path %q is outside the configured roots", clean)
	}

	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	if !f.underRoot(resolved) {
		return "", fmt.Errorf("path %q resolves outside the configured roots", clean)
	}
	return resolved, nil
}

// rootPaths is what a path is compared against: every configured root, and
// the symlink-resolved form of each.
//
// The resolved form matters because the path being checked has already been
// through EvalSymlinks. If a root is itself a symlink — /srv pointing at
// external storage, which is the ordinary shape on a Pi — then every file
// under it resolved to a path outside the literal root and was refused as
// "outside the configured roots" while sitting plainly inside one. That failed
// closed, so nothing leaked; it just captured nothing, and said so in a status
// that reads like a misconfiguration on the operator's part.
//
// Adding the resolved root does not widen the allowlist: it is the same
// directory, named the way the filesystem names it.
func (f *FileReader) rootPaths() []string {
	f.once.Do(func() {
		for _, root := range f.Roots {
			if root == "" {
				continue
			}
			clean := filepath.Clean(root)
			f.resolved = append(f.resolved, clean)
			if actual, err := filepath.EvalSymlinks(clean); err == nil && actual != clean {
				f.resolved = append(f.resolved, actual)
			}
		}
	})
	return f.resolved
}

func (f *FileReader) underRoot(path string) bool {
	for _, root := range f.rootPaths() {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		// filepath.Rel yields a ".." prefix for anything above the root.
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// FilesFingerprint hashes the captured set, so a file edit registers as a
// change even when nothing about the running stack moved.
func FilesFingerprint(files []CapturedFile) string {
	h := sha256.New()
	for _, f := range sortedFiles(files) {
		fmt.Fprintf(h, "%s:%s:%s\n", f.Path, f.Status, contentHash(f.Content))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func contentHash(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func sortedFiles(files []CapturedFile) []CapturedFile {
	out := append([]CapturedFile(nil), files...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Path < out[j-1].Path; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
