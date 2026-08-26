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
	"sync"
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
//
// The keep-list is editable from the settings screen while the collector is
// running, so it sits behind a mutex. The key never changes: it is generated
// once per install, and rotating it would orphan every digest already stored.
type Redactor struct {
	key []byte

	mu       sync.RWMutex
	keepKeys []string
}

// New returns a Redactor using key for HMAC and the built-in keep-list plus
// extra. Patterns may use a single leading or trailing '*'.
func New(key []byte, extra []string) *Redactor {
	return &Redactor{key: key, keepKeys: buildKeepList(extra)}
}

// SetKeepKeys replaces the extra keep-list. The built-in entries are always
// included, so an empty list narrows Silt to its defaults rather than
// redacting everything.
func (r *Redactor) SetKeepKeys(extra []string) {
	next := buildKeepList(extra)
	r.mu.Lock()
	r.keepKeys = next
	r.mu.Unlock()
}

func buildKeepList(extra []string) []string {
	keep := make([]string, 0, len(defaultKeepKeys)+len(extra))
	for _, k := range defaultKeepKeys {
		keep = append(keep, strings.ToUpper(k))
	}
	for _, k := range extra {
		if k = strings.ToUpper(strings.TrimSpace(k)); k != "" {
			keep = append(keep, k)
		}
	}
	return keep
}

// Keep reports whether a key's value may be stored in cleartext.
func (r *Redactor) Keep(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return false
	}
	r.mu.RLock()
	patterns := r.keepKeys
	r.mu.RUnlock()
	for _, pattern := range patterns {
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

// Reason explains why a line's value was kept or hidden, for the marking UI.
type Reason string

const (
	// ReasonStructure means the line holds no value to redact.
	ReasonStructure Reason = "structure"
	// ReasonInterpolation means the value is a ${VAR} reference — a pointer to
	// a secret, not the secret, and worth seeing when it changes.
	ReasonInterpolation Reason = "interpolation"
	// ReasonKeepList means the key is on the built-in safe list.
	ReasonKeepList Reason = "keep_list"
	// ReasonDefault means it was hidden because nothing said to keep it.
	ReasonDefault Reason = "default"
	// ReasonRuleHide and ReasonRuleReveal mean a person decided.
	ReasonRuleHide   Reason = "rule_hide"
	ReasonRuleReveal Reason = "rule_reveal"
)

// LinePolicy overrides the built-in keep-list for a specific file.
//
// It exists so a person can correct the guess in both directions: hide a key
// the list missed, reveal one it over-hid.
type LinePolicy interface {
	// Decide returns whether to keep the value in cleartext, and true if a
	// rule actually applied. A false second return means no rule matched and
	// the keep-list decides.
	Decide(lineNo int, key string) (keep bool, matched bool)
}

// Line describes one line of a redacted file, for the marking UI.
type Line struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	// Key is the assignment key on this line, when there is one. Rules keyed
	// on it survive edits that move the line.
	Key string `json:"key,omitempty"`
	// Redacted is true when this line's value was replaced.
	Redacted bool   `json:"redacted"`
	Reason   Reason `json:"reason"`
	// Markable is false for lines with no value to decide about.
	Markable bool `json:"markable"`
}

// ComposeText redacts the raw text of a compose or .env file while preserving
// its line structure exactly, and reports what it did to each line.
//
// This is what makes storing the files safe. A compose file can carry literal
// secrets — an inline `POSTGRES_PASSWORD: hunter2`, a token in a command
// argument — and a .env file is nothing but secrets. Storing them verbatim
// would break the one promise the project rests on.
//
// Line structure is preserved so a line diff still answers the question people
// actually ask: which line changed. The value shows as a keyed digest on both
// sides, so a changed secret is visibly a change without the secret being
// recoverable. Everything that is not a value — keys, indentation, image
// references, ports, comments — stays exactly as written.
//
// The same function serves the capture path and the marking preview, so what
// someone sees while choosing what to hide is exactly what would be stored.
func (r *Redactor) ComposeText(content []byte, policy LinePolicy) ([]byte, []Line) {
	raw := strings.Split(string(content), "\n")
	out := make([]string, len(raw))
	info := make([]Line, len(raw))

	// inEnv tracks whether we are inside an `environment:` block, identified by
	// indentation: entries indented further than the key belong to it.
	inEnv := false
	envIndent := 0

	for i, line := range raw {
		lineNo := i + 1
		out[i] = line
		info[i] = Line{Number: lineNo, Text: line, Reason: ReasonStructure}

		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if inEnv && indent <= envIndent {
			inEnv = false
		}
		if isEnvBlockStart(trimmed) {
			inEnv = true
			envIndent = indent
			continue
		}

		key, value, ok := assignment(trimmed, inEnv)
		if !ok || value == "" {
			continue
		}

		replacement, redacted, reason := r.decide(lineNo, key, value, policy)
		out[i] = replaceValue(line, value, replacement)
		info[i] = Line{
			Number:   lineNo,
			Text:     out[i],
			Key:      strings.TrimSpace(key),
			Redacted: redacted,
			Reason:   reason,
			Markable: true,
		}
	}
	return []byte(strings.Join(out, "\n")), info
}

// decide resolves one value against the rules and the keep-list, in that
// order: a person's explicit choice beats a built-in guess in both directions.
func (r *Redactor) decide(lineNo int, key, value string, policy LinePolicy) (replacement string, redacted bool, reason Reason) {
	// A ${VAR} reference is not a secret — it is a pointer to one, and seeing
	// which variable a service reads is exactly the kind of change worth
	// noticing.
	if isInterpolation(value) {
		return value, false, ReasonInterpolation
	}

	if policy != nil {
		if keep, matched := policy.Decide(lineNo, strings.TrimSpace(key)); matched {
			if keep {
				return value, false, ReasonRuleReveal
			}
			return r.Placeholder(unquote(value)), true, ReasonRuleHide
		}
	}

	if r.Keep(key) {
		return value, false, ReasonKeepList
	}
	return r.Placeholder(unquote(value)), true, ReasonDefault
}

// assignment extracts the key and value from a line, in whichever of the forms
// a compose or .env file uses.
func assignment(trimmed string, inEnv bool) (key, value string, ok bool) {
	item := strings.TrimPrefix(trimmed, "- ")
	listForm := item != trimmed
	item = strings.TrimPrefix(item, "export ")

	if k, v, found := strings.Cut(item, "="); found {
		// A YAML key whose value merely contains '=' is not an assignment.
		if strings.Contains(k, ":") {
			return "", "", false
		}
		return k, v, true
	}
	// The mapping form (`KEY: value`) only counts inside an environment block;
	// elsewhere it is YAML structure like `image: nginx:1.25`.
	if !inEnv || listForm {
		return "", "", false
	}
	k, v, found := strings.Cut(item, ":")
	if !found {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

// isEnvBlockStart reports whether a line opens an environment block.
func isEnvBlockStart(trimmed string) bool {
	return trimmed == "environment:" || trimmed == "- environment:"
}

// isInterpolation reports whether a value is entirely a ${VAR} reference.
func isInterpolation(value string) bool {
	v := strings.TrimSpace(unquote(value))
	return strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") &&
		!strings.Contains(v[2:len(v)-1], "${")
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// replaceValue swaps the last occurrence of old in line, keeping surrounding
// whitespace, quoting and any trailing comment untouched.
func replaceValue(line, old, replacement string) string {
	if old == replacement {
		return line
	}
	i := strings.LastIndex(line, old)
	if i < 0 {
		return line
	}
	return line[:i] + replacement + line[i+len(old):]
}
