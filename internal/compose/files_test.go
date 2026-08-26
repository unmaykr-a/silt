package compose_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/redact"
)

func newReader(t *testing.T, roots ...string) *compose.FileReader {
	t.Helper()
	return &compose.FileReader{
		Roots:    roots,
		MaxBytes: 1 << 20,
		Redactor: redact.New([]byte("test-key"), nil),
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCaptureRedactsAndCounts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "media", "compose.yaml")
	write(t, path, "services:\n  a:\n    environment:\n      - API_KEY=secret\n")

	files := newReader(t, root).Capture([]string{path}, nil)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	f := files[0]
	if f.Status != compose.FileOK {
		t.Fatalf("status = %q, want ok", f.Status)
	}
	if strings.Contains(string(f.Content), "secret") {
		t.Errorf("captured content holds the secret:\n%s", f.Content)
	}
	if f.LineCount != 5 {
		t.Errorf("line count = %d, want 5", f.LineCount)
	}
}

// The paths Silt follows come from container labels, which anyone who can
// start a container controls. Without a root check a crafted label could point
// it at any file the process can read.
func TestCaptureRefusesPathsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secrets.env")
	write(t, outside, "ROOT_PASSWORD=hunter2\n")

	files := newReader(t, root).Capture([]string{outside}, nil)
	if files[0].Status != compose.FileOutsideRoots {
		t.Errorf("status = %q, want outside_roots", files[0].Status)
	}
	if len(files[0].Content) != 0 {
		t.Errorf("content was captured despite being outside the roots: %s", files[0].Content)
	}
}

func TestCaptureRefusesTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "roots")
	write(t, filepath.Join(root, "keep.yaml"), "services: {}\n")
	sibling := filepath.Join(filepath.Dir(root), "secrets.env")
	write(t, sibling, "TOKEN=hunter2\n")

	traversal := filepath.Join(root, "..", "secrets.env")
	files := newReader(t, root).Capture([]string{traversal}, nil)
	if files[0].Status == compose.FileOK {
		t.Errorf("a ../ traversal was captured: %s", files[0].Content)
	}
}

// Checking only the literal path would be defeated by a symlink inside a
// mounted root pointing anywhere on the filesystem.
func TestCaptureRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secrets.env")
	write(t, outside, "TOKEN=hunter2\n")

	link := filepath.Join(root, "sneaky.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	files := newReader(t, root).Capture([]string{link}, nil)
	if files[0].Status == compose.FileOK {
		t.Errorf("a symlink escaping the root was captured: %s", files[0].Content)
	}
	if strings.Contains(string(files[0].Content), "hunter2") {
		t.Error("symlink escape leaked file content")
	}
}

func TestCaptureRefusesOversizedFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.yaml")
	write(t, path, strings.Repeat("x", 2048))

	reader := newReader(t, root)
	reader.MaxBytes = 1024
	files := reader.Capture([]string{path}, nil)
	if files[0].Status != compose.FileTooLarge {
		t.Errorf("status = %q, want too_large", files[0].Status)
	}
}

func TestCaptureReportsMissingFiles(t *testing.T) {
	root := t.TempDir()
	files := newReader(t, root).Capture([]string{filepath.Join(root, "gone.yaml")}, nil)
	if files[0].Status != compose.FileUnreadable {
		t.Errorf("status = %q, want unreadable", files[0].Status)
	}
}

// With no roots configured, nothing is read at all.
func TestDisabledReaderCapturesNothing(t *testing.T) {
	reader := &compose.FileReader{MaxBytes: 1 << 20, Redactor: redact.New([]byte("k"), nil)}
	if reader.Enabled() {
		t.Fatal("a reader with no roots reports itself enabled")
	}
	files := reader.Capture([]string{"/etc/passwd"}, nil)
	if files[0].Status == compose.FileOK {
		t.Error("a disabled reader captured a file")
	}
}

func TestRulesApplyDuringCapture(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "compose.yaml")
	write(t, path, "services:\n  a:\n    environment:\n      - PUID=1000\n")

	files := newReader(t, root).Capture([]string{path}, []compose.Rule{
		{Path: path, Action: compose.ActionHide, Kind: compose.KindKey, Key: "PUID"},
	})
	if strings.Contains(string(files[0].Content), "PUID=1000") {
		t.Errorf("a hide rule was not applied at capture time:\n%s", files[0].Content)
	}
}

// A rule with no path applies across the project, which is how someone hides a
// key like SMTP_PASSWORD everywhere at once.
func TestProjectWideRuleAppliesToEveryFile(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.yaml")
	b := filepath.Join(root, "b.yaml")
	write(t, a, "services:\n  x:\n    environment:\n      - TZ=Europe/Tallinn\n")
	write(t, b, "services:\n  y:\n    environment:\n      - TZ=Europe/Tallinn\n")

	files := newReader(t, root).Capture([]string{a, b}, []compose.Rule{
		{Action: compose.ActionHide, Kind: compose.KindKey, Key: "TZ"},
	})
	for _, f := range files {
		if strings.Contains(string(f.Content), "Europe/Tallinn") {
			t.Errorf("project-wide rule missed %s:\n%s", f.Path, f.Content)
		}
	}
}

// The fingerprint has to move when a file changes and hold still when it does
// not, or an edited file would either go unnoticed or look like constant churn.
func TestFilesFingerprint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "compose.yaml")
	write(t, path, "services:\n  a:\n    image: nginx:1.24\n")
	reader := newReader(t, root)

	first := compose.FilesFingerprint(reader.Capture([]string{path}, nil))
	again := compose.FilesFingerprint(reader.Capture([]string{path}, nil))
	if first != again {
		t.Error("fingerprint changed without the file changing")
	}

	write(t, path, "services:\n  a:\n    image: nginx:1.25\n")
	edited := compose.FilesFingerprint(reader.Capture([]string{path}, nil))
	if edited == first {
		t.Error("fingerprint did not change after the file was edited")
	}
}

func TestPreviewReportsWhyItCannotRead(t *testing.T) {
	root := t.TempDir()
	reader := newReader(t, root)

	if _, err := reader.Preview("/etc/passwd", nil); err == nil {
		t.Error("preview accepted a path outside the roots")
	} else if !strings.Contains(err.Error(), "compose root") {
		t.Errorf("error should say why: %v", err)
	}

	disabled := &compose.FileReader{MaxBytes: 1, Redactor: redact.New([]byte("k"), nil)}
	if _, err := disabled.Preview("/anything", nil); err == nil {
		t.Error("preview worked with no roots configured")
	}
}

// Preview must show exactly what capture would store, or someone would be
// marking against a different thing than gets written.
func TestPreviewMatchesCapture(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "compose.yaml")
	write(t, path, "services:\n  a:\n    environment:\n      - API_KEY=secret\n      - PUID=1000\n")
	reader := newReader(t, root)

	preview, err := reader.Preview(path, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	captured := reader.Capture([]string{path}, nil)[0]

	if string(preview.Content) != string(captured.Content) {
		t.Errorf("preview and capture disagree:\npreview:\n%s\ncapture:\n%s", preview.Content, captured.Content)
	}
}
