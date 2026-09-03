// Package diff compares two project observations and classifies what changed.
//
// Diffs are computed server-side: both models are already normalised, so the
// comparison is a straight structural walk and the browser only has to render
// the result. See PROJECT.md Section 6.
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/redact"
)

// Kind classifies a change. These drive both UI colour and the notification
// filter, so they are a closed set rather than free text.
type Kind string

const (
	KindImageRef      Kind = "image_ref"
	KindImageID       Kind = "image_id"
	KindImageDigest   Kind = "image_digest"
	KindEnv           Kind = "env"
	KindPorts         Kind = "ports"
	KindVolumes       Kind = "volumes"
	KindNetworks      Kind = "networks"
	KindHealthcheck   Kind = "healthcheck"
	KindResources     Kind = "resources"
	KindCommand       Kind = "command"
	KindEntrypoint    Kind = "entrypoint"
	KindRestartPolicy Kind = "restart_policy"
	KindLabels        Kind = "labels"
	KindDependsOn     Kind = "depends_on"
	KindServiceAdded  Kind = "service_added"
	KindServiceRemove Kind = "service_removed"
	KindState         Kind = "state"
	KindOther         Kind = "other"
)

// AllKinds is every change kind Silt can produce.
//
// Exported so the notification filter can refuse a kind that does not exist,
// rather than accepting it and matching nothing: `SILT_NOTIFY_ON=image` is a
// plausible typo for `image_id`, and it used to mean "never notify" with no
// error anywhere — discovered during the outage the notification was for.
var AllKinds = []Kind{
	KindImageRef, KindImageID, KindImageDigest, KindEnv, KindPorts,
	KindVolumes, KindNetworks, KindHealthcheck, KindResources, KindCommand,
	KindEntrypoint, KindRestartPolicy, KindLabels, KindDependsOn,
	KindServiceAdded, KindServiceRemove, KindState, KindOther,
}

// ValidKind reports whether k is a kind Silt actually produces.
func ValidKind(k Kind) bool {
	for _, known := range AllKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Severity drives UI colour and the notification threshold.
type Severity string

const (
	High   Severity = "high"
	Medium Severity = "medium"
	Low    Severity = "low"
)

// severityOf implements the heuristic from PROJECT.md Section 8.
//
// image_id sits at high alongside image_digest: it is the identity the
// fingerprint actually uses, and a changed image is the case Silt exists to
// catch. image_ref is low because a retag without a new image is cosmetic.
func severityOf(k Kind) Severity {
	switch k {
	case KindImageID, KindImageDigest, KindVolumes, KindServiceRemove:
		return High
	case KindEnv, KindPorts, KindNetworks, KindCommand, KindEntrypoint,
		KindHealthcheck, KindServiceAdded, KindDependsOn:
		return Medium
	case KindLabels, KindResources, KindImageRef, KindRestartPolicy, KindState:
		return Low
	default:
		return Low
	}
}

// Op is what happened to a path.
type Op string

const (
	OpAdd     Op = "add"
	OpRemove  Op = "remove"
	OpReplace Op = "replace"
)

// Change is one classified difference.
type Change struct {
	Kind     Kind     `json:"kind"`
	Service  string   `json:"service,omitempty"`
	Path     string   `json:"path"`
	Op       Op       `json:"op"`
	Before   string   `json:"before,omitempty"`
	After    string   `json:"after,omitempty"`
	Severity Severity `json:"severity"`
}

// Side identifies one end of a comparison.
type Side struct {
	SnapshotID int64 `json:"id"`
	TakenAt    int64 `json:"taken_at"`
}

// Runtime is the volatile state of one service, carried separately from the
// project model so a config diff never depends on it.
type Runtime struct {
	State        string
	Health       string
	RestartCount int
}

// Input is one snapshot's worth of comparable data.
type Input struct {
	Side     Side
	Project  compose.Project
	Runtimes map[string]Runtime
}

// Result is the full comparison.
type Result struct {
	From    Side         `json:"from"`
	To      Side         `json:"to"`
	Summary map[Kind]int `json:"summary"`
	Changes []Change     `json:"changes"`
}

// Compute compares two observations.
//
// Output order is deterministic — service, then kind, then path — so the same
// pair always renders identically and the response can be cached.
func Compute(from, to Input) Result {
	res := Result{
		From:    from.Side,
		To:      to.Side,
		Summary: map[Kind]int{},
		Changes: []Change{},
	}

	for _, name := range unionKeys(from.Project.Services, to.Project.Services) {
		before, inBefore := from.Project.Services[name]
		after, inAfter := to.Project.Services[name]

		switch {
		case !inBefore:
			res.add(Change{
				Kind:    KindServiceAdded,
				Service: name,
				Path:    "services." + name,
				Op:      OpAdd,
				After:   after.Image,
			})
		case !inAfter:
			res.add(Change{
				Kind:    KindServiceRemove,
				Service: name,
				Path:    "services." + name,
				Op:      OpRemove,
				Before:  before.Image,
			})
		default:
			res.diffService(name, before, after)
		}
	}

	res.diffRuntimes(from, to)

	sort.SliceStable(res.Changes, func(i, j int) bool {
		a, b := res.Changes[i], res.Changes[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Path < b.Path
	})
	return res
}

func (r *Result) add(c Change) {
	if c.Severity == "" {
		c.Severity = severityOf(c.Kind)
	}
	r.Changes = append(r.Changes, c)
	r.Summary[c.Kind]++
}

func (r *Result) diffService(name string, before, after compose.Service) {
	p := func(field string) string { return "services." + name + "." + field }

	r.scalar(name, KindImageRef, p("image"), before.Image, after.Image)
	r.scalar(name, KindImageID, p("image_id"), before.ImageID, after.ImageID)
	r.scalar(name, KindImageDigest, p("image_digest"), before.ImageDigest, after.ImageDigest)
	r.scalar(name, KindRestartPolicy, p("restart_policy"), before.RestartPolicy, after.RestartPolicy)
	r.scalar(name, KindOther, p("working_dir"), before.WorkingDir, after.WorkingDir)
	r.scalar(name, KindOther, p("user"), before.User, after.User)

	// Environment values here are already redacted placeholders, so a diff can
	// report that a secret changed without either side ever holding it.
	r.mapDiff(name, KindEnv, p("environment"), before.Environment, after.Environment)
	r.mapDiff(name, KindLabels, p("labels"), before.Labels, after.Labels)

	r.setDiff(name, KindPorts, p("ports"), before.Ports, after.Ports)
	r.setDiff(name, KindPorts, p("exposed_ports"), before.ExposedPorts, after.ExposedPorts)
	r.setDiff(name, KindNetworks, p("networks"), before.Networks, after.Networks)
	r.setDiff(name, KindDependsOn, p("depends_on"), before.DependsOn, after.DependsOn)
	r.setDiff(name, KindOther, p("cap_add"), before.CapAdd, after.CapAdd)
	r.setDiff(name, KindOther, p("cap_drop"), before.CapDrop, after.CapDrop)
	r.mountDiff(name, p("volumes"), before.Volumes, after.Volumes)

	// Order is meaningful for these, so they compare as a whole.
	r.listDiff(name, KindCommand, p("command"), before.Command, after.Command)
	r.listDiff(name, KindEntrypoint, p("entrypoint"), before.Entrypoint, after.Entrypoint)
	r.listDiff(name, KindHealthcheck, p("healthcheck"), before.Healthcheck, after.Healthcheck)

	if before.Privileged != after.Privileged {
		r.add(Change{
			Kind: KindOther, Service: name, Path: p("privileged"), Op: OpReplace,
			Before: fmt.Sprint(before.Privileged), After: fmt.Sprint(after.Privileged),
			// Gaining privileged is a security-relevant change, not a footnote.
			Severity: High,
		})
	}
	r.scalar(name, KindResources, p("memory_limit"), num(before.MemoryLimit), num(after.MemoryLimit))
	r.scalar(name, KindResources, p("nano_cpus"), num(before.NanoCPUs), num(after.NanoCPUs))
}

func (r *Result) diffRuntimes(from, to Input) {
	for _, name := range unionKeys(from.Runtimes, to.Runtimes) {
		b, inB := from.Runtimes[name]
		a, inA := to.Runtimes[name]
		if !inB || !inA {
			// A service appearing or disappearing is already reported as
			// service_added/service_removed; not worth a second entry.
			continue
		}
		base := "services." + name + "."
		r.scalar(name, KindState, base+"state", b.State, a.State)
		r.scalar(name, KindState, base+"health", b.Health, a.Health)
		if b.RestartCount != a.RestartCount {
			r.add(Change{
				Kind: KindState, Service: name, Path: base + "restart_count", Op: OpReplace,
				Before: num(int64(b.RestartCount)), After: num(int64(a.RestartCount)),
			})
		}
	}
}

func (r *Result) scalar(service string, kind Kind, path, before, after string) {
	if before == after {
		return
	}
	r.add(Change{
		Kind: kind, Service: service, Path: path,
		Op:     opFor(before != "", after != ""),
		Before: before, After: after,
	})
}

// mapDiff reports per-key additions, removals and replacements.
func (r *Result) mapDiff(service string, kind Kind, base string, before, after map[string]string) {
	for _, key := range unionKeys(before, after) {
		b, inB := before[key]
		a, inA := after[key]
		if inB && inA && b == a {
			continue
		}
		r.add(Change{
			Kind: kind, Service: service, Path: base + "." + key,
			Op:     opFor(inB, inA),
			Before: b, After: a,
		})
	}
}

// setDiff treats the slices as sets, which is right for anything whose order
// carries no meaning — ports, networks, volumes. Both sides are already sorted
// by normalisation, so a reorder is invisible here, which is the point.
func (r *Result) setDiff(service string, kind Kind, base string, before, after []string) {
	inBefore := toSet(before)
	inAfter := toSet(after)

	for _, v := range sortedUnion(inBefore, inAfter) {
		_, b := inBefore[v]
		_, a := inAfter[v]
		if b == a {
			continue
		}
		c := Change{Kind: kind, Service: service, Path: base, Op: opFor(b, a)}
		if b {
			c.Before = v
		} else {
			c.After = v
		}
		r.add(c)
	}
}

// listDiff compares order-sensitive lists as a single value.
func (r *Result) listDiff(service string, kind Kind, path string, before, after []string) {
	b := strings.Join(before, " ")
	a := strings.Join(after, " ")
	r.scalar(service, kind, path, b, a)
}

// mountDiff keys mounts by their container-side target rather than comparing
// them as an opaque set.
//
// A bind whose host source changed is one fact — "the /config mount moved" —
// not two unrelated ones. Set semantics would report it as a removal and an
// addition, which reads as a bigger change than it is and loses the
// before/after pairing that makes it legible.
func (r *Result) mountDiff(service, path string, before, after []redact.Mount) {
	byTarget := func(ms []redact.Mount) map[string]redact.Mount {
		out := make(map[string]redact.Mount, len(ms))
		for _, m := range ms {
			out[m.Target] = m
		}
		return out
	}
	b, a := byTarget(before), byTarget(after)

	for _, target := range unionKeys(b, a) {
		bm, inB := b[target]
		am, inA := a[target]
		if inB && inA && mountString(bm) == mountString(am) {
			continue
		}
		c := Change{Kind: KindVolumes, Service: service, Path: path + "." + target, Op: opFor(inB, inA)}
		if inB {
			c.Before = mountString(bm)
		}
		if inA {
			c.After = mountString(am)
		}
		r.add(c)
	}
}

func mountString(m redact.Mount) string {
	return fmt.Sprintf("%s %s -> %s (%s)", m.Type, m.Source, m.Target, m.Mode)
}

func opFor(hadBefore, hasAfter bool) Op {
	switch {
	case !hadBefore && hasAfter:
		return OpAdd
	case hadBefore && !hasAfter:
		return OpRemove
	default:
		return OpReplace
	}
}

func num(v int64) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprint(v)
}

func toSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, v := range in {
		out[v] = struct{}{}
	}
	return out
}

func sortedUnion(a, b map[string]struct{}) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]struct{}{a, b} {
		for k := range m {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// unionKeys returns the sorted union of two maps' keys.
func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]V{a, b} {
		for k := range m {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
