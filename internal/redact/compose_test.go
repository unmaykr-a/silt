package redact_test

import (
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/redact"
)

func testRedactor(extra ...string) *redact.Redactor {
	return redact.New([]byte("test-install-key-32-bytes-long!!"), extra)
}

const sample = `# Media stack
services:
  radarr:
    image: lscr.io/linuxserver/radarr:latest
    ports:
      - "7878:7878"
    environment:
      - PUID=1000
      - TZ=Europe/Tallinn
      - API_KEY=hunter2supersecret
      - DB_PASSWORD=${POSTGRES_PASSWORD}
    volumes:
      - /srv/media/radarr:/config
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: media
      POSTGRES_PASSWORD: literalsecret
`

func lineWith(t *testing.T, text, needle string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, text)
	return ""
}

func TestComposeTextHidesSecretsAndKeepsStructure(t *testing.T) {
	out, lines := testRedactor().ComposeText([]byte(sample), nil)
	text := string(out)

	for _, secret := range []string{"hunter2supersecret", "literalsecret"} {
		if strings.Contains(text, secret) {
			t.Errorf("secret %q survived redaction:\n%s", secret, text)
		}
	}

	// Line structure is the whole point: a diff has to be able to say which
	// line changed.
	if got, want := len(lines), len(strings.Split(sample, "\n")); got != want {
		t.Errorf("line count changed from %d to %d", want, got)
	}
	if strings.Count(text, "\n") != strings.Count(sample, "\n") {
		t.Error("newline count changed")
	}

	// Everything that is not a value stays exactly as written.
	for _, structural := range []string{
		"# Media stack",
		"    image: lscr.io/linuxserver/radarr:latest",
		`      - "7878:7878"`,
		"      - /srv/media/radarr:/config",
		"    image: postgres:16",
	} {
		if !strings.Contains(text, structural) {
			t.Errorf("structural line was altered; expected to find:\n%s\ngot:\n%s", structural, text)
		}
	}
}

func TestComposeTextKeepsSafeKeysReadable(t *testing.T) {
	out, _ := testRedactor().ComposeText([]byte(sample), nil)
	text := string(out)

	if !strings.Contains(text, "- PUID=1000") {
		t.Errorf("PUID was redacted; the keep-list should have kept it:\n%s", text)
	}
	if !strings.Contains(text, "- TZ=Europe/Tallinn") {
		t.Errorf("TZ was redacted:\n%s", text)
	}
}

// A ${VAR} reference points at a secret rather than being one, and seeing
// which variable a service reads is exactly the kind of change worth noticing.
func TestInterpolationReferencesSurvive(t *testing.T) {
	out, _ := testRedactor().ComposeText([]byte(sample), nil)
	if !strings.Contains(string(out), "- DB_PASSWORD=${POSTGRES_PASSWORD}") {
		t.Errorf("a ${VAR} reference was redacted:\n%s", out)
	}
}

// The mapping form is redacted too, not just the list form.
func TestMappingFormEnvironmentIsRedacted(t *testing.T) {
	out, _ := testRedactor().ComposeText([]byte(sample), nil)
	line := lineWith(t, string(out), "POSTGRES_PASSWORD:")
	if !strings.Contains(line, "[redacted:") {
		t.Errorf("mapping-form value was not redacted: %q", line)
	}
	// The key and its indentation survive.
	if !strings.HasPrefix(line, "      POSTGRES_PASSWORD: ") {
		t.Errorf("mapping-form line lost its shape: %q", line)
	}
}

// An image tag contains a colon and must not be mistaken for a mapping value.
func TestImageTagsAreNotTreatedAsAssignments(t *testing.T) {
	out, _ := testRedactor().ComposeText([]byte("services:\n  a:\n    image: nginx:1.25-alpine\n"), nil)
	if !strings.Contains(string(out), "image: nginx:1.25-alpine") {
		t.Errorf("an image tag was redacted:\n%s", out)
	}
}

// A .env file is nothing but assignments and is the highest-risk file of all.
func TestDotEnvFileIsRedactedLineByLine(t *testing.T) {
	env := "# secrets\nPUID=1000\nPOSTGRES_PASSWORD=hunter2\nexport SMTP_TOKEN=abc123\n\nEMPTY=\n"
	out, _ := testRedactor().ComposeText([]byte(env), nil)
	text := string(out)

	if strings.Contains(text, "hunter2") || strings.Contains(text, "abc123") {
		t.Errorf("a .env secret survived:\n%s", text)
	}
	if !strings.Contains(text, "PUID=1000") {
		t.Errorf("PUID should stay readable:\n%s", text)
	}
	if !strings.Contains(text, "# secrets") {
		t.Errorf("a comment was altered:\n%s", text)
	}
	if !strings.Contains(text, "EMPTY=") {
		t.Errorf("an empty value was mangled:\n%s", text)
	}
}

// A changed secret must produce a changed line, or the diff would hide the one
// thing it exists to show.
func TestChangedSecretProducesADifferentLine(t *testing.T) {
	r := testRedactor()
	before, _ := r.ComposeText([]byte("environment:\n  - API_KEY=one\n"), nil)
	after, _ := r.ComposeText([]byte("environment:\n  - API_KEY=two\n"), nil)

	if string(before) == string(after) {
		t.Error("two different secrets produced identical redacted lines")
	}
	if strings.Contains(string(before)+string(after), "one") {
		t.Error("a secret leaked into the redacted output")
	}
}

// The same secret must produce the same line, or every capture would look like
// a change.
func TestUnchangedSecretIsStable(t *testing.T) {
	r := testRedactor()
	a, _ := r.ComposeText([]byte("environment:\n  - API_KEY=same\n"), nil)
	b, _ := r.ComposeText([]byte("environment:\n  - API_KEY=same\n"), nil)
	if string(a) != string(b) {
		t.Error("an unchanged secret produced a different line on re-capture")
	}
}

// fixedPolicy is a stand-in for the manual marking rules.
type fixedPolicy struct {
	keys  map[string]bool // key -> keep
	lines map[int]bool    // line -> keep
}

func (p fixedPolicy) Decide(lineNo int, key string) (bool, bool) {
	if keep, ok := p.keys[key]; ok {
		return keep, true
	}
	if keep, ok := p.lines[lineNo]; ok {
		return keep, true
	}
	return false, false
}

// Marking works in both directions: hide what the keep-list missed, reveal
// what it over-hid.
func TestRulesOverrideTheKeepListBothWays(t *testing.T) {
	r := testRedactor()
	input := []byte("environment:\n  - PUID=1000\n  - APP_REGION=eu-north\n")

	// By default PUID is readable and APP_REGION is not.
	base, _ := r.ComposeText(input, nil)
	if !strings.Contains(string(base), "PUID=1000") {
		t.Fatalf("baseline changed:\n%s", base)
	}
	if strings.Contains(string(base), "eu-north") {
		t.Fatalf("APP_REGION should be hidden by default:\n%s", base)
	}

	// Hiding a kept key and revealing a hidden one.
	out, lines := r.ComposeText(input, fixedPolicy{keys: map[string]bool{
		"PUID":       false, // hide
		"APP_REGION": true,  // reveal
	}})
	text := string(out)
	if strings.Contains(text, "PUID=1000") {
		t.Errorf("a hide rule did not hide PUID:\n%s", text)
	}
	if !strings.Contains(text, "APP_REGION=eu-north") {
		t.Errorf("a reveal rule did not reveal APP_REGION:\n%s", text)
	}

	var sawHide, sawReveal bool
	for _, l := range lines {
		switch l.Reason {
		case redact.ReasonRuleHide:
			sawHide = true
		case redact.ReasonRuleReveal:
			sawReveal = true
		}
	}
	if !sawHide || !sawReveal {
		t.Errorf("line reasons did not report the applied rules: hide=%v reveal=%v", sawHide, sawReveal)
	}
}

func TestLineRulesApplyByNumber(t *testing.T) {
	r := testRedactor()
	// Line 2 is PUID, normally kept.
	out, _ := r.ComposeText([]byte("environment:\n  - PUID=1000\n"), fixedPolicy{lines: map[int]bool{2: false}})
	if strings.Contains(string(out), "PUID=1000") {
		t.Errorf("a line rule did not hide line 2:\n%s", out)
	}
}

// Line metadata drives the marking UI, so it has to describe reality.
func TestLineMetadataDescribesEachLine(t *testing.T) {
	_, lines := testRedactor().ComposeText([]byte(sample), nil)

	byKey := map[string]redact.Line{}
	for _, l := range lines {
		if l.Key != "" {
			byKey[l.Key] = l
		}
	}

	if l := byKey["API_KEY"]; !l.Redacted || l.Reason != redact.ReasonDefault || !l.Markable {
		t.Errorf("API_KEY line = %+v, want redacted by default and markable", l)
	}
	if l := byKey["PUID"]; l.Redacted || l.Reason != redact.ReasonKeepList {
		t.Errorf("PUID line = %+v, want kept by the keep-list", l)
	}
	if l := byKey["DB_PASSWORD"]; l.Redacted || l.Reason != redact.ReasonInterpolation {
		t.Errorf("DB_PASSWORD line = %+v, want kept as an interpolation", l)
	}

	// A structural line offers nothing to decide about.
	for _, l := range lines {
		if strings.Contains(l.Text, "image: postgres:16") && l.Markable {
			t.Errorf("an image line was offered as markable: %+v", l)
		}
	}
}
