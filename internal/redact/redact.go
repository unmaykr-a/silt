// Package redact keeps secret values out of everything Silt persists.
//
// The threat model is explicit: someone obtains silt.db — a leaked backup, a
// misconfigured volume, a shared debug bundle. Nothing in the database may let
// them recover an environment value.
//
// Two decisions carry that guarantee, and both are inversions of the obvious
// design. See PROJECT.md Section 7.
package redact

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

// Placeholder text used in stored compose blobs in place of a real value.
const placeholderPrefix = "[redacted:"

// Length buckets. The exact length of a secret is free entropy for anyone
// guessing it and buys the UI nothing, so only a bucket is stored.
const (
	BucketEmpty  = "empty"
	BucketShort  = "short"  // 1-8
	BucketMedium = "medium" // 9-32
	BucketLong   = "long"   // 33+
)

// defaultKeepKeys are environment keys whose values are safe in cleartext.
//
// This is a keep-list, not a redact-list, and the direction matters more than
// the contents. A redact-list fails open: `PW=hunter2` matches no regex of
// pass|secret|token, looks harmless to an entropy check, and lands on disk in
// cleartext. Every such default is one unanticipated key name away from a
// breach. Redacting by default fails closed, and the worst case is a value
// that could have been readable but is not.
var defaultKeepKeys = []string{
	"PUID", "PGID", "UID", "GID", "TZ", "UMASK",
	"LANG", "LANGUAGE", "LC_*", "TERM", "PATH", "HOME", "HOSTNAME",
	"NODE_ENV", "RAILS_ENV", "APP_ENV", "ENVIRONMENT", "ENV",
	"LOG_LEVEL", "LOGLEVEL", "DEBUG", "VERBOSE",
	"PORT", "*_PORT", "*_TIMEOUT", "*_INTERVAL", "*_RETRIES",
	"PYTHONUNBUFFERED", "GOMAXPROCS", "JAVA_OPTS",
	"PGDATA", "DOCKER_HOST",

	// Structural label namespaces. Compose's own labels are how Silt
	// discovers anything at all, and OCI image labels are definitionally
	// public metadata; redacting either would break the model to no benefit.
	"COM.DOCKER.COMPOSE.*",
	"ORG.OPENCONTAINERS.IMAGE.*",
	"ORG.LABEL-SCHEMA.*",
	"MAINTAINER", "DESCRIPTION",
}

// Redactor decides what is kept and hashes what is not.
type Redactor struct {
	key      []byte
	keepKeys []string
}

// New returns a Redactor using key for HMAC and the built-in keep-list plus
// extra. Patterns may use a single leading or trailing '*'.
func New(key []byte, extra []string) *Redactor {
	keep := make([]string, 0, len(defaultKeepKeys)+len(extra))
	for _, k := range defaultKeepKeys {
		keep = append(keep, strings.ToUpper(k))
	}
	for _, k := range extra {
		if k = strings.ToUpper(strings.TrimSpace(k)); k != "" {
			keep = append(keep, k)
		}
	}
	return &Redactor{key: key, keepKeys: keep}
}

// Keep reports whether a key's value may be stored in cleartext.
func (r *Redactor) Keep(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return false
	}
	for _, pattern := range r.keepKeys {
		if ok, err := path.Match(pattern, upper); err == nil && ok {
			return true
		}
	}
	return false
}

// Sum returns the first 12 hex characters of HMAC-SHA256(key, value).
//
// HMAC rather than a bare hash, because sha256(value)[:12] is a guessing
// oracle: a four-digit PIN is ten thousand hashes and `hunter2` is in every
// wordlist. Keyed with a per-install secret, the digests stay comparable
// within one install — all the "did this change?" query needs — and are
// useless to anyone holding the database without the key.
func (r *Redactor) Sum(value string) string {
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))[:12]
}

// Bucket classifies a value's length.
func Bucket(value string) string {
	switch n := len(value); {
	case n == 0:
		return BucketEmpty
	case n <= 8:
		return BucketShort
	case n <= 32:
		return BucketMedium
	default:
		return BucketLong
	}
}

// Placeholder is what appears in a stored compose or inspect blob in place of
// a redacted value. ASCII only: the value is re-rendered as YAML and pasted
// into shells, so non-ASCII guillemets would be a needless encoding hazard.
func (r *Redactor) Placeholder(value string) string {
	return placeholderPrefix + r.Sum(value) + "]"
}

// Value is one environment entry after redaction.
type Value struct {
	Key string
	// Display is what may appear in a stored blob: the cleartext value for a
	// kept key, the placeholder otherwise.
	Display string
	// Cleartext is the stored value, empty unless Redacted is false.
	Cleartext string
	Sum       string
	Bucket    string
	Redacted  bool
}

// Env redacts a single KEY=VALUE pair.
func (r *Redactor) Env(key, value string) Value {
	v := Value{
		Key:    key,
		Sum:    r.Sum(value),
		Bucket: Bucket(value),
	}
	if r.Keep(key) {
		v.Redacted = false
		v.Cleartext = value
		v.Display = value
		return v
	}
	v.Redacted = true
	v.Display = r.Placeholder(value)
	return v
}

// EnvPair splits a "KEY=VALUE" entry as Docker reports it in Config.Env.
// A entry with no '=' is treated as a key with an empty value.
func EnvPair(entry string) (key, value string) {
	if i := strings.IndexByte(entry, '='); i >= 0 {
		return entry[:i], entry[i+1:]
	}
	return entry, ""
}

// EnvSlice redacts a Docker-style []string of "KEY=VALUE" entries.
func (r *Redactor) EnvSlice(entries []string) []Value {
	out := make([]Value, 0, len(entries))
	for _, e := range entries {
		k, v := EnvPair(e)
		out = append(out, r.Env(k, v))
	}
	return out
}

// Strings redacts values that may carry secrets but have no key to judge by —
// command lines, entrypoints, bind mount paths. Anything shaped like
// KEY=VALUE is redacted per the keep-list; everything else is passed through,
// since a command's arguments are usually the whole point of recording it.
func (r *Redactor) Strings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if k, v := EnvPair(s); strings.Contains(s, "=") && !r.Keep(k) && v != "" {
			out = append(out, k+"="+r.Placeholder(v))
			continue
		}
		out = append(out, s)
	}
	return out
}

// Labels redacts a label map by the same rule as environment values.
//
// Labels are config by convention, but secrets do live there — a Traefik
// basicauth middleware stores password hashes in a label. Failing closed
// costs some diff fidelity on labels that were never sensitive; operators who
// want their router rules readable can widen SILT_KEEP_KEYS, which is a
// deliberate choice rather than an unnoticed default.
func (r *Redactor) Labels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if r.Keep(k) || v == "" {
			out[k] = v
			continue
		}
		out[k] = r.Placeholder(v)
	}
	return out
}

// Mount is one mount in redacted form.
type Mount struct {
	Type   string `json:"type"`
	Source string `json:"source,omitempty"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
}

// MountInput is the pre-redaction form, matching docker.Mount.
type MountInput struct {
	Type   string
	Source string
	Target string
	Mode   string
}

// Mounts redacts the host-side source of bind mounts.
//
// Type, target and mode are pure configuration and are always kept, as is the
// source of a named volume — that is a Compose-generated volume name, not a
// host path. The host path of a bind mount is redacted.
//
// This is stricter than it looks like it needs to be, and the cost is real: a
// volumes diff will say that the source of a bind changed without saying what
// it changed to. The alternative was a heuristic guessing which path segments
// look like credentials, and an entropy check that cannot tell
// "storage-2023-archive-01" from a hex token is not a guarantee — it is a
// coin flip dressed up as one. Silt's promise is that the database holds no
// recoverable secret, so where there is no key to judge a value by, the value
// is redacted.
func (r *Redactor) Mounts(in []MountInput) []Mount {
	out := make([]Mount, 0, len(in))
	for _, m := range in {
		red := Mount{Type: m.Type, Target: m.Target, Mode: m.Mode}
		switch {
		case m.Source == "":
			// tmpfs and anonymous volumes have no source.
		case m.Type == "volume":
			red.Source = m.Source
		default:
			red.Source = r.Placeholder(m.Source)
		}
		out = append(out, red)
	}
	return out
}
