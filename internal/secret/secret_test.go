package secret_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/secret"
)

func TestSealedValuesOpenBackToThemselves(t *testing.T) {
	c, err := secret.New("a key of no particular length")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, want := range []string{
		"a-token",
		`["ntfy://user:pass@ntfy.example/silt"]`,
		"unicode ✓ and a newline\n",
		strings.Repeat("x", 4096),
	} {
		sealed, err := c.Seal(want)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if sealed == want {
			t.Errorf("seal left %q unchanged", want)
		}
		if !strings.HasPrefix(sealed, secret.Prefix) {
			t.Errorf("sealed value does not carry the prefix: %q", sealed)
		}
		got, err := c.Open(sealed)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if got != want {
			t.Errorf("round trip gave %q, want %q", got, want)
		}
	}
}

// The nonce is fresh per call, so the same secret stored twice does not produce
// the same bytes — otherwise a backup would show that two installs share a token.
func TestSealingTheSameValueTwiceDiffers(t *testing.T) {
	c, _ := secret.New("k")
	a, _ := c.Seal("same")
	b, _ := c.Seal("same")
	if a == b {
		t.Error("two seals of one value are identical; the nonce is not fresh")
	}
}

// No key is the upgrade path: an install that has never set one keeps working,
// and its values stay exactly as they were.
func TestWithoutAKeyNothingChanges(t *testing.T) {
	c, err := secret.New("")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if c.Enabled() {
		t.Error("a cipher with no key reports itself enabled")
	}
	sealed, err := c.Seal("a-token")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed != "a-token" {
		t.Errorf("seal without a key changed the value to %q", sealed)
	}
	if got, _ := c.Open("a-token"); got != "a-token" {
		t.Errorf("open without a key changed the value to %q", got)
	}
}

// Adding a key to an existing install has to read what is already there.
func TestAValueStoredBeforeTheKeyStillReads(t *testing.T) {
	c, _ := secret.New("k")
	got, err := c.Open("a-plaintext-token")
	if err != nil {
		t.Fatalf("open a legacy value: %v", err)
	}
	if got != "a-plaintext-token" {
		t.Errorf("got %q, want the value unchanged", got)
	}
}

// Removing the key must say so rather than hand the ciphertext onwards. Silt
// would otherwise present "enc:v1:…" to an identity provider as a client secret
// and report an authentication failure, sending someone to read their provider's
// logs instead of their own compose file.
func TestRemovingTheKeyIsReportedNotSwallowed(t *testing.T) {
	with, _ := secret.New("k")
	sealed, _ := with.Seal("a-token")

	without, _ := secret.New("")
	if _, err := without.Open(sealed); err == nil {
		t.Fatal("opening a sealed value with no key succeeded")
	} else if !strings.Contains(err.Error(), "SILT_SECRET_KEY") {
		t.Errorf("the error does not name the variable to set: %v", err)
	}
}

// A changed key is the same shape of mistake and needs the same answer.
func TestTheWrongKeyIsRefused(t *testing.T) {
	a, _ := secret.New("first")
	b, _ := secret.New("second")
	sealed, _ := a.Seal("a-token")
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("a value sealed with one key opened with another")
	}
}

// Tampering has to fail rather than decrypt to something. It is what makes this
// AES-GCM rather than a stream cipher: anyone who can edit the database row can
// already do worse, but a value that silently became different bytes would be a
// setting nobody could explain.
//
// The flip happens on the decoded bytes rather than on the base64, and that is
// not fussiness. The first version of this test flipped the last base64
// character, which passed most of the time and failed about one run in four:
// RawStdEncoding's final character carries only part of a byte, so several
// characters decode identically and the "tampered" value was often the original.
func TestATamperedValueIsRefused(t *testing.T) {
	c, err := secret.New("k")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	sealed, err := c.Seal("a-token")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, secret.Prefix))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The last byte is inside the authentication tag, so this is a forgery
	// attempt rather than a corrupted nonce.
	raw[len(raw)-1] ^= 0x01
	tampered := secret.Prefix + base64.RawStdEncoding.EncodeToString(raw)

	if tampered == sealed {
		t.Fatal("the tamper did not change the value")
	}
	if _, err := c.Open(tampered); err == nil {
		t.Error("a tampered ciphertext opened successfully")
	}
}

// Empty stays empty: a ciphertext for "" would make an unset secret
// indistinguishable from one deliberately cleared, and clearing is how the
// ingest endpoint is turned off.
func TestAnEmptyValueIsNotSealed(t *testing.T) {
	c, _ := secret.New("k")
	if sealed, _ := c.Seal(""); sealed != "" {
		t.Errorf("seal(\"\") = %q, want empty", sealed)
	}
}
