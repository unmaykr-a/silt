package demo

import (
	"regexp"
	"testing"
)

// Internal, so the helpers do not need exporting to be tested. They are the
// only two pieces of the seed that are logic rather than data.

func TestBumpTagLooksLikeAnUpgrade(t *testing.T) {
	// Every tag shape the seed actually contains. The "-alt" suffix the old
	// seed appended was visibly synthetic in the screenshot the project leads
	// with, which is what this replaced.
	cases := []struct{ in, want string }{
		{"lscr.io/linuxserver/radarr:5.4.0", "lscr.io/linuxserver/radarr:5.4.1"},
		{"redis:7", "redis:8"},
		{"alpine:3.20", "alpine:3.21"},
		{"prom/prometheus:v2.54", "prom/prometheus:v2.55"},
		{"tensorchord/pgvecto-rs:pg16", "tensorchord/pgvecto-rs:pg17"},
		{"ghcr.io/paperless-ngx/paperless-ngx:2.11", "ghcr.io/paperless-ngx/paperless-ngx:2.12"},
		// Digits that are not at the end. Looking only at the tail left this
		// stack with an unchanging image and a single-row history.
		{"nextcloud:29-apache", "nextcloud:30-apache"},
		{"ghcr.io/home-assistant/home-assistant:2024.9", "ghcr.io/home-assistant/home-assistant:2024.10"},
		// Zero padding survives, so a date-shaped tag stays date-shaped.
		{"pihole/pihole:2024.07", "pihole/pihole:2024.08"},
		// Nothing to advance, rather than something invented.
		{"someimage:latest", "someimage:latest"},
		{"noTagAtAll", "noTagAtAll"},
		{"trailingcolon:", "trailingcolon:"},
	}
	for _, c := range cases {
		if got := bumpTag(c.in); got != c.want {
			t.Errorf("bumpTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFakeDigestLooksLikeADigest(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)

	a := fakeDigest("lscr.io/linuxserver/radarr:5.4.0")
	b := fakeDigest("lscr.io/linuxserver/radarr:5.6.0")

	for _, d := range []string{a, b} {
		if !hex.MatchString(d) {
			t.Errorf("digest %q is not 64 hex characters", d)
		}
	}
	if a == b {
		t.Error("two different images produced the same digest")
	}
	if a != fakeDigest("lscr.io/linuxserver/radarr:5.4.0") {
		t.Error("the same image produced two different digests")
	}
}
