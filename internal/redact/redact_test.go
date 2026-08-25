package redact

import (
	"strings"
	"testing"
)

func testRedactor(extra ...string) *Redactor {
	return New([]byte("test-install-key-32-bytes-long!!"), extra)
}

// The keep-list must fail closed. Every case here is a key that a
// secret-matching regex would have missed.
func TestKeepListFailsClosed(t *testing.T) {
	r := testRedactor()
	mustRedact := []string{
		"PW", "PASSWD", "SMTP_LOGIN", "ADMIN_EMAIL", "MYSQL_USER",
		"HASS_TOKEN", "S3_BUCKET", "WEBHOOK", "CONN", "DSN",
		"POSTGRES_PASSWORD", "API_KEY", "PRIVATE_KEY", "SALT",
		"", "  ", "UNKNOWN_THING", "MY_CUSTOM_VAR",
	}
	for _, key := range mustRedact {
		if r.Keep(key) {
			t.Errorf("Keep(%q) = true; unknown keys must be redacted", key)
		}
	}
}

func TestKeepListAllowsKnownSafeKeys(t *testing.T) {
	r := testRedactor()
	mustKeep := []string{
		"PUID", "PGID", "TZ", "UMASK", "LOG_LEVEL", "DEBUG",
		"NODE_ENV", "PORT", "HTTP_PORT", "READ_TIMEOUT",
		"LC_ALL", "puid", "  TZ  ",
		"com.docker.compose.project", "com.docker.compose.service",
		"org.opencontainers.image.version",
	}
	for _, key := range mustKeep {
		if !r.Keep(key) {
			t.Errorf("Keep(%q) = false; want kept", key)
		}
	}
}

func TestKeepKeysExtension(t *testing.T) {
	r := testRedactor("MY_CUSTOM_VAR", "APP_*")
	for _, key := range []string{"MY_CUSTOM_VAR", "APP_REGION", "app_region"} {
		if !r.Keep(key) {
			t.Errorf("Keep(%q) = false; SILT_KEEP_KEYS should have allowed it", key)
		}
	}
	if r.Keep("OTHER_VAR") {
		t.Error("extension leaked to unrelated keys")
	}
}

func TestEnvRedactsUnknownKeys(t *testing.T) {
	r := testRedactor()
	v := r.Env("POSTGRES_PASSWORD", "hunter2")

	if !v.Redacted {
		t.Fatal("POSTGRES_PASSWORD was not redacted")
	}
	if v.Cleartext != "" {
		t.Errorf("Cleartext = %q, want empty", v.Cleartext)
	}
	if strings.Contains(v.Display, "hunter2") {
		t.Errorf("Display %q contains the secret", v.Display)
	}
	if !strings.HasPrefix(v.Display, "[redacted:") {
		t.Errorf("Display = %q, want a [redacted:...] placeholder", v.Display)
	}
	if v.Sum == "" || len(v.Sum) != 12 {
		t.Errorf("Sum = %q, want 12 hex chars", v.Sum)
	}
}

func TestEnvKeepsSafeKeys(t *testing.T) {
	r := testRedactor()
	v := r.Env("PUID", "1000")
	if v.Redacted {
		t.Fatal("PUID was redacted; the motivating readable case must survive")
	}
	if v.Cleartext != "1000" || v.Display != "1000" {
		t.Errorf("got cleartext=%q display=%q, want 1000", v.Cleartext, v.Display)
	}
}

// The digest must be keyed. A bare sha256 of a low-entropy value is a
// guessing oracle; two installs must not produce the same digest.
func TestSumIsKeyedPerInstall(t *testing.T) {
	a := New([]byte("install-a-key"), nil)
	b := New([]byte("install-b-key"), nil)

	if a.Sum("hunter2") == b.Sum("hunter2") {
		t.Error("two installs produced the same digest for one value; the key is not being used")
	}
	// Within one install, digests must be stable so change detection works.
	if a.Sum("hunter2") != a.Sum("hunter2") {
		t.Error("digest is not stable within an install")
	}
	if a.Sum("hunter2") == a.Sum("hunter3") {
		t.Error("different values produced the same digest")
	}
}

func TestBucketHidesExactLength(t *testing.T) {
	tests := map[string]string{
		"":                       BucketEmpty,
		"a":                      BucketShort,
		"12345678":               BucketShort,
		"123456789":              BucketMedium,
		strings.Repeat("x", 32):  BucketMedium,
		strings.Repeat("x", 33):  BucketLong,
		strings.Repeat("x", 500): BucketLong,
	}
	for value, want := range tests {
		if got := Bucket(value); got != want {
			t.Errorf("Bucket(len %d) = %q, want %q", len(value), got, want)
		}
	}
	// Two different secrets of different exact lengths must be
	// indistinguishable when they share a bucket.
	if Bucket("1234") != Bucket("12345") {
		t.Error("bucket distinguishes exact lengths within a band")
	}
}

func TestStringsRedactsInlineAssignments(t *testing.T) {
	r := testRedactor()
	got := r.Strings([]string{"--token=s3cret", "serve", "--port=8375", "PUID=1000"})

	joined := strings.Join(got, " ")
	if strings.Contains(joined, "s3cret") {
		t.Errorf("command retained a secret: %v", got)
	}
	if !strings.Contains(joined, "serve") {
		t.Errorf("plain arguments must survive: %v", got)
	}
	if !strings.Contains(joined, "PUID=1000") {
		t.Errorf("kept keys must survive in commands: %v", got)
	}
}

func TestLabelsKeepStructuralNamespaces(t *testing.T) {
	r := testRedactor()
	got := r.Labels(map[string]string{
		"com.docker.compose.project":                    "media",
		"com.docker.compose.service":                    "radarr",
		"traefik.http.middlewares.auth.basicauth.users": "admin:$$apr1$$hunter2hash",
		"org.opencontainers.image.version":              "1.2.3",
	})
	if got["com.docker.compose.project"] != "media" {
		t.Error("compose labels must survive; discovery depends on them")
	}
	if got["org.opencontainers.image.version"] != "1.2.3" {
		t.Error("OCI image labels must survive")
	}
	if strings.Contains(got["traefik.http.middlewares.auth.basicauth.users"], "hunter2hash") {
		t.Error("a basicauth label leaked its hash")
	}
}

func TestEnvPair(t *testing.T) {
	tests := []struct{ in, key, value string }{
		{"KEY=value", "KEY", "value"},
		{"KEY=", "KEY", ""},
		{"KEY=a=b", "KEY", "a=b"},
		{"BARE", "BARE", ""},
		{"=leading", "", "leading"},
	}
	for _, tt := range tests {
		k, v := EnvPair(tt.in)
		if k != tt.key || v != tt.value {
			t.Errorf("EnvPair(%q) = (%q, %q), want (%q, %q)", tt.in, k, v, tt.key, tt.value)
		}
	}
}
