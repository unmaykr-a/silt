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
