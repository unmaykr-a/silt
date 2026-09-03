package notify

import (
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/diff"
)

func change(kind diff.Kind, severity diff.Severity, service string) diff.Change {
	return diff.Change{Kind: kind, Severity: severity, Service: service, Path: "p", Op: diff.OpReplace}
}

func TestParseFilterDefaults(t *testing.T) {
	f, err := ParseFilter([]string{"image_id", "volumes"}, "")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if f.MinSeverity != diff.Medium {
		t.Errorf("MinSeverity = %q, want medium", f.MinSeverity)
	}
	if !f.Kinds[diff.KindImageID] || !f.Kinds[diff.KindVolumes] {
		t.Errorf("kinds not parsed: %+v", f.Kinds)
	}
}

func TestParseFilterRejectsUnknownSeverity(t *testing.T) {
	if _, err := ParseFilter(nil, "catastrophic"); err == nil {
		t.Error("ParseFilter accepted an unknown severity")
	}
}

func TestParseFilterAllKinds(t *testing.T) {
	f, err := ParseFilter([]string{"all"}, "low")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if f.Kinds != nil {
		t.Error(`"all" should mean every kind, not a set`)
	}
	matched := f.Match([]diff.Change{change(diff.KindLabels, diff.Low, "a")})
	if len(matched) != 1 {
		t.Errorf("low-severity label change did not match an all/low filter")
	}
}

// Kinds and severity are ANDed. Either alone lets through more than anyone
// wants: a homelab running Watchtower produces image changes constantly.
func TestFilterAndsKindAndSeverity(t *testing.T) {
	f, err := ParseFilter([]string{"image_id", "env"}, "high")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}

	matched := f.Match([]diff.Change{
		change(diff.KindImageID, diff.High, "radarr"), // right kind, meets threshold
		change(diff.KindEnv, diff.Medium, "radarr"),   // right kind, below threshold
		change(diff.KindVolumes, diff.High, "radarr"), // wrong kind, meets threshold
		change(diff.KindLabels, diff.Low, "radarr"),   // neither
	})

	if len(matched) != 1 {
		t.Fatalf("matched %d changes, want 1: %+v", len(matched), matched)
	}
	if matched[0].Kind != diff.KindImageID {
		t.Errorf("matched the wrong change: %+v", matched[0])
	}
}

func TestFilterMatchesNothingWhenNoKindsListed(t *testing.T) {
	f, err := ParseFilter([]string{}, "low")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if got := f.Match([]diff.Change{change(diff.KindImageID, diff.High, "a")}); len(got) != 0 {
		t.Errorf("an empty kind list matched %d changes; it should match none", len(got))
	}
}

// A nil Sender must be a working no-op, so an install with no configured
// targets needs no branch at the call site.
func TestNilSenderIsSafe(t *testing.T) {
	sender, err := New(nil, Filter{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sender != nil {
		t.Fatal("New with no URLs should return a nil sender")
	}
	sender.Notify(t.Context(), Change{Project: "media"})
}

func TestNewIgnoresBlankURLs(t *testing.T) {
	sender, err := New([]string{"", "  "}, Filter{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sender != nil {
		t.Error("blank URLs should not produce a sender")
	}
}

// A shoutrrr URL carries the credential for the service it targets, so a
// configuration error must not echo it.
func TestNewErrorDoesNotLeakURLs(t *testing.T) {
	secret := "supersecrettoken"
	_, err := New([]string{"notarealscheme://" + secret + "@example.com"}, Filter{}, nil)
	if err == nil {
		t.Fatal("expected an error for an unknown scheme")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaked the notification URL: %v", err)
	}
}

func TestFormatGroupsByService(t *testing.T) {
	title, body := format(
		Change{Project: "media", SnapshotID: 42, FromID: 41, BaseURL: "https://silt.example.com/"},
		[]diff.Change{
			change(diff.KindImageID, diff.High, "sonarr"),
			change(diff.KindImageID, diff.High, "radarr"),
			change(diff.KindEnv, diff.Medium, "radarr"),
		},
	)

	if !strings.Contains(title, "media") {
		t.Errorf("title = %q, want the project name", title)
	}
	// Services are grouped and ordered, so a stack-wide update reads as one
	// event rather than a wall of lines.
	radarr := strings.Index(body, "radarr")
	sonarr := strings.Index(body, "sonarr")
	if radarr < 0 || sonarr < 0 || radarr > sonarr {
		t.Errorf("services not grouped in order:\n%s", body)
	}
	if !strings.Contains(body, "/diff?from=41&to=42") {
		t.Errorf("body has no link to the diff:\n%s", body)
	}
	if strings.Contains(body, "//diff") {
		t.Errorf("trailing slash in base URL produced a doubled path:\n%s", body)
	}
}

func TestFormatOmitsLinkWithoutBaseURL(t *testing.T) {
	_, body := format(Change{Project: "media", SnapshotID: 2, FromID: 1}, []diff.Change{
		change(diff.KindImageID, diff.High, "radarr"),
	})
	if strings.Contains(body, "http") {
		t.Errorf("body invented a link without a base URL:\n%s", body)
	}
}

// The settings screen reads these back, so the mask is the thing standing
// between "look at the UI" and "read the webhook tokens".
func TestMaskKeepsTheSchemeAndNeverTheCredential(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"discord://tok3n@webhookid", "discord://…"},
		{"telegram://bottoken@telegram?chats=1234", "telegram://…"},
		{"gotify://gotify.example.com/AppTokenHere", "gotify://gotify.example.com/…"},
		{"smtp://user:pass@mail.example.com:587/?from=a&to=b", "smtp://mail.example.com:587/…"},
		{"ntfy://ntfy.sh/mytopic", "ntfy://ntfy.sh/…"},
		{"not a url", "…"},
	}
	for _, tc := range cases {
		if got := Mask(tc.in); got != tc.want {
			t.Errorf("Mask(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMaskAllDropsBlanks(t *testing.T) {
	got := MaskAll([]string{"discord://a@b", "  ", ""})
	if len(got) != 1 {
		t.Fatalf("MaskAll = %v, want one entry", got)
	}
}

// Swapping targets at runtime must not leave a half-applied configuration: a
// bad URL keeps the previous sender rather than silencing notifications.
func TestLiveKeepsThePreviousSenderWhenAReplacementIsInvalid(t *testing.T) {
	good, err := New([]string{"logger://"}, Filter{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	live := NewLive(good)
	if !live.Enabled() {
		t.Fatal("live sender reports itself disabled")
	}
	if err := live.Replace([]string{"notarealscheme://x"}, Filter{}, nil); err == nil {
		t.Fatal("an unknown scheme was accepted")
	}
	if !live.Enabled() {
		t.Error("a rejected replacement silenced the previous sender")
	}
}

// An install that starts with nothing configured still has to be able to turn
// notifications on without a restart.
func TestLiveCanGoFromNothingToConfigured(t *testing.T) {
	live := NewLive(nil)
	if live.Enabled() {
		t.Fatal("an empty live sender reports itself enabled")
	}
	live.Notify(t.Context(), Change{Project: "media"})
	if err := live.Replace([]string{"logger://"}, Filter{}, nil); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if !live.Enabled() {
		t.Error("Replace did not install the new sender")
	}
}

// A kind that does not exist used to be accepted and then match nothing, so a
// typo meant "never notify" and you found out during the outage.
func TestParseFilterRefusesAnUnknownKind(t *testing.T) {
	for _, kinds := range [][]string{
		{"image"},  // a plausible typo for image_id
		{"images"}, // and another
		{"image_id", "ports", "voluems"},
		{"IMAGE_ID_TYPO"},
	} {
		if _, err := ParseFilter(kinds, "medium"); err == nil {
			t.Errorf("ParseFilter(%v) accepted a kind Silt never produces", kinds)
		}
	}
}

func TestParseFilterAcceptsEveryRealKind(t *testing.T) {
	names := make([]string, 0, len(diff.AllKinds))
	for _, k := range diff.AllKinds {
		names = append(names, string(k))
	}
	if _, err := ParseFilter(names, "low"); err != nil {
		t.Errorf("ParseFilter refused the full list of real kinds: %v", err)
	}
	// The wildcards still mean everything.
	for _, all := range [][]string{{"all"}, {"*"}, {"ALL"}} {
		f, err := ParseFilter(all, "low")
		if err != nil {
			t.Errorf("ParseFilter(%v): %v", all, err)
		}
		if f.Kinds != nil {
			t.Errorf("ParseFilter(%v) did not mean every kind", all)
		}
	}
}

// The error has to say what is allowed, or the fix is a documentation hunt.
func TestParseFilterErrorNamesTheValidKinds(t *testing.T) {
	_, err := ParseFilter([]string{"image"}, "medium")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "image_id") {
		t.Errorf("the error does not list the valid kinds: %v", err)
	}
}
