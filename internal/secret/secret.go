// Package secret encrypts the small credentials Silt keeps in its database.
//
// Silt stores editable settings as a JSON document in one row, and some of those
// settings are credentials: the ingest token, the shoutrrr notification targets
// — each of which carries the credential for the service it points at — and,
// since 1.2.0, the OpenID Connect client secret. They were readable in that row
// in plaintext, which matters more here than it would elsewhere, because Silt
// hands out whole-database copies: GET /api/backup is a consistent snapshot of
// the file, and a backup is a thing people keep, copy to another machine, and
// forget about.
//
// So SILT_SECRET_KEY, which stays in the environment and is never written to the
// database or included in a backup. With it set, a leaked backup yields
// ciphertext. Without it, behaviour is exactly what it was — the cipher is a
// no-op and values pass through — because a key that is required breaks every
// install that upgrades without setting one.
//
// This is not the same mechanism as internal/redact, and the distinction is
// worth keeping straight. Redaction is one-way: an environment value Silt
// observed is replaced by a keyed digest and the original is never stored, so
// there is nothing to decrypt and nothing to leak. These are settings Silt has
// to be able to use — it must present the real client secret to the provider —
// so they are encrypted rather than digested, which is a weaker guarantee and
// only as good as keeping the key out of the database.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Prefix marks an encrypted value, so ciphertext is distinguishable from a value
// stored before a key was configured. That is what lets a key be added to an
// existing install: old values are read as they are and re-written encrypted on
// the next save.
const Prefix = "enc:v1:"

var encoding = base64.RawStdEncoding

// Cipher seals and opens values. The zero value is a working no-op, so a caller
// never has to branch on whether a key is configured.
type Cipher struct {
	aead cipher.AEAD
}

// New derives an AES-256-GCM cipher from key, which may be any length — it is
// hashed to 32 bytes. An empty key yields a no-op cipher.
func New(key string) (*Cipher, error) {
	if key == "" {
		return &Cipher{}, nil
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Enabled reports whether a key is configured.
func (c *Cipher) Enabled() bool { return c != nil && c.aead != nil }

// Seal returns a self-describing ciphertext, or the input unchanged when no key
// is configured. An empty value stays empty: there is nothing to hide and a
// ciphertext would make "not set" indistinguishable from "set to nothing".
func (c *Cipher) Seal(plaintext string) (string, error) {
	if !c.Enabled() || plaintext == "" {
		return plaintext, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("seal: %w", err)
	}
	return Prefix + encoding.EncodeToString(c.aead.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

// Open reverses Seal. A value without the prefix is returned as it is: either it
// was stored before a key was configured, or no key is configured now.
//
// A prefixed value with no key is an error rather than a pass-through. Returning
// the ciphertext would mean Silt presenting "enc:v1:…" to an identity provider
// as a client secret and reporting an authentication failure, which sends
// someone to look at their provider rather than at the key they removed.
func (c *Cipher) Open(value string) (string, error) {
	if !strings.HasPrefix(value, Prefix) {
		return value, nil
	}
	if !c.Enabled() {
		return "", errors.New("a stored value is encrypted but SILT_SECRET_KEY is not set")
	}
	raw, err := encoding.DecodeString(strings.TrimPrefix(value, Prefix))
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	size := c.aead.NonceSize()
	if len(raw) < size {
		return "", errors.New("open: ciphertext is too short")
	}
	plaintext, err := c.aead.Open(nil, raw[:size], raw[size:], nil)
	if err != nil {
		// Wrong key or tampered data; GCM does not distinguish, and neither
		// should the message.
		return "", errors.New("open: a stored value could not be decrypted with SILT_SECRET_KEY")
	}
	return string(plaintext), nil
}
