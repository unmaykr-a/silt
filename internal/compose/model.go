// Package compose builds the effective configuration model of a Compose
// project, normalises it, and redacts it.
//
// v1 derives the model from what is actually running, via container labels and
// docker inspect. That path needs no mounts, is always available, and
// describes the stack as it is rather than as a file on disk claims it to be.
// Loading the compose files themselves is enrichment, and lands in M2.5.
// See PROJECT.md Section 5.
package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
)

// Sources a project model can be built from.
const (
	SourceContainers  = "containers"
	SourceFiles       = "files"
	SourceUnavailable = "unavailable"
)

// Project is the redacted, normalised model of one Compose project.
//
// Maps are used wherever order is not semantically meaningful, because
// encoding/json sorts map keys — which is most of what canonical output
// needs. Slices that reach this struct are pre-sorted by the normalisation in
// internal/docker.
type Project struct {
	Name        string             `json:"name"`
	WorkingDir  string             `json:"working_dir,omitempty"`
	ConfigFiles []string           `json:"config_files,omitempty"`
	Source      string             `json:"source"`
	Services    map[string]Service `json:"services"`
}

// Service is one service's effective configuration. Runtime state lives in
// ServiceRuntime and is deliberately absent here: this struct is what the
// config fingerprint hashes.
type Service struct {
	Image         string            `json:"image,omitempty"`
	ImageID       string            `json:"image_id,omitempty"`
	ImageDigest   string            `json:"image_digest,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty"`
	User          string            `json:"user,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Ports         []string          `json:"ports,omitempty"`
	ExposedPorts  []string          `json:"exposed_ports,omitempty"`
	Volumes       []redact.Mount    `json:"volumes,omitempty"`
	Networks      []string          `json:"networks,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`
	Privileged    bool              `json:"privileged,omitempty"`
	CapAdd        []string          `json:"cap_add,omitempty"`
	CapDrop       []string          `json:"cap_drop,omitempty"`
	DependsOn     []string          `json:"depends_on,omitempty"`
	Healthcheck   []string          `json:"healthcheck,omitempty"`
	MemoryLimit   int64             `json:"memory_limit,omitempty"`
	NanoCPUs      int64             `json:"nano_cpus,omitempty"`
}

// ServiceRuntime is the volatile half, kept out of the project model.
type ServiceRuntime struct {
	Service       string
	ContainerID   string
	ContainerName string
	ImageRef      string
	ImageID       string
	ImageDigest   string
	ImageCreated  int64
	State         string
	Health        string
	RestartCount  int
	StartedAt     *int64
	// ExitCode is set only for a container that has stopped: it says whether
	// someone stopped it or it died. OOMKilled separates the most common
	// cause of a 137 from a plain SIGKILL, which share an exit code and are
	// very different problems.
	ExitCode    *int
	OOMKilled   bool
	InspectHash string
	// EnvKeys is the per-key redaction record for the env_keys table.
	EnvKeys []redact.Value
}

// Observation is one complete look at a project.
type Observation struct {
	Project  Project
	Runtimes []ServiceRuntime
	// InspectBlobs maps service name to the canonical JSON of its redacted
	// config subset, to be stored as blobs.
	InspectBlobs map[string][]byte
	// Files are the project's compose and .env files, already redacted.
	Files []CapturedFile
}

// ServiceInput is one container's contribution to a project observation.
type ServiceInput struct {
	Service      string
	Inspected    docker.Inspected
	ImageDigest  string
	ImageCreated int64
}

// Build assembles a redacted project model from inspected containers.
func Build(p docker.Project, inputs []ServiceInput, r *redact.Redactor) (Observation, error) {
	obs := Observation{
		Project: Project{
			Name:        p.Name,
			WorkingDir:  p.WorkingDir,
			ConfigFiles: p.ConfigFiles,
			Source:      SourceContainers,
			Services:    make(map[string]Service, len(inputs)),
		},
		InspectBlobs: make(map[string][]byte, len(inputs)),
	}
	if len(inputs) == 0 {
		obs.Project.Source = SourceUnavailable
	}

	// Deterministic order for the runtime slice; the model itself is a map and
	// sorts itself when marshalled.
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Service < inputs[j].Service })

	for _, in := range inputs {
		cfg := in.Inspected.Config

		// Redaction happens here, before anything is marshalled, hashed,
		// stored or logged. Nothing downstream sees a raw value.
		envValues := r.EnvSlice(cfg.Env)
		env := make(map[string]string, len(envValues))
		for _, v := range envValues {
			env[v.Key] = v.Display
		}

		svc := Service{
			Image:         cfg.Image,
			ImageID:       cfg.ImageID,
			ImageDigest:   in.ImageDigest,
			Environment:   env,
			Command:       r.Strings(cfg.Cmd),
			Entrypoint:    r.Strings(cfg.Entrypoint),
			WorkingDir:    cfg.WorkingDir,
			User:          cfg.User,
			Labels:        r.Labels(cfg.Labels),
			Ports:         cfg.PortBindings,
			ExposedPorts:  cfg.ExposedPorts,
			Volumes:       r.Mounts(toMountInputs(cfg.Mounts)),
			Networks:      cfg.Networks,
			RestartPolicy: cfg.RestartPolicy,
			Privileged:    cfg.Privileged,
			CapAdd:        cfg.CapAdd,
			CapDrop:       cfg.CapDrop,
			DependsOn:     cfg.DependsOn,
			Healthcheck:   r.Strings(cfg.Healthcheck),
			MemoryLimit:   cfg.MemoryLimit,
			NanoCPUs:      cfg.NanoCPUs,
		}
		obs.Project.Services[in.Service] = svc

		blob, err := CanonicalJSON(svc)
		if err != nil {
			return Observation{}, fmt.Errorf("marshal inspect for %s: %w", in.Service, err)
		}
		obs.InspectBlobs[in.Service] = blob

		rt := in.Inspected.Runtime
		obs.Runtimes = append(obs.Runtimes, ServiceRuntime{
			Service:       in.Service,
			ContainerID:   rt.ContainerID,
			ContainerName: rt.ContainerName,
			ImageRef:      cfg.Image,
			ImageID:       cfg.ImageID,
			ImageDigest:   in.ImageDigest,
			ImageCreated:  in.ImageCreated,
			State:         rt.State,
			Health:        rt.Health,
			RestartCount:  rt.RestartCount,
			StartedAt:     rt.StartedAt,
			ExitCode:      rt.ExitCode,
			OOMKilled:     rt.OOMKilled,
			EnvKeys:       envValues,
		})
	}
	return obs, nil
}

// CanonicalJSON marshals v deterministically. encoding/json sorts map keys,
// and every slice that reaches the model is pre-sorted, so the output is
// stable across observations of an unchanged stack.
//
// Skipping this is the single biggest way to make Silt useless: unnormalised
// output makes every key reorder look like a change.
func CanonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// ConfigFingerprint hashes the compose blob together with each service's image
// identity and inspect blob hash.
func ConfigFingerprint(composeHash string, runtimes []ServiceRuntime) string {
	h := sha256.New()
	fmt.Fprintf(h, "compose:%s\n", composeHash)
	for _, rt := range sortedRuntimes(runtimes) {
		fmt.Fprintf(h, "svc:%s image:%s inspect:%s\n", rt.Service, rt.ImageID, rt.InspectHash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RuntimeFingerprint hashes only volatile state.
//
// Keeping this separate is what stops a crash-looping container from earning
// the long retention tier and firing a notification on every restart.
func RuntimeFingerprint(runtimes []ServiceRuntime) string {
	h := sha256.New()
	for _, rt := range sortedRuntimes(runtimes) {
		started := int64(0)
		if rt.StartedAt != nil {
			started = *rt.StartedAt
		}
		// The exit code is part of the runtime state: a container that exited
		// 0 and later exited 137 has changed in a way worth recording, and
		// without this the second stop would be indistinguishable from the
		// first and get touched onto the existing snapshot.
		exit := "none"
		if rt.ExitCode != nil {
			exit = strconv.Itoa(*rt.ExitCode)
		}
		fmt.Fprintf(h, "svc:%s state:%s health:%s restarts:%d started:%d exit:%s oom:%t\n",
			rt.Service, rt.State, rt.Health, rt.RestartCount, started, exit, rt.OOMKilled)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func toMountInputs(in []docker.Mount) []redact.MountInput {
	out := make([]redact.MountInput, 0, len(in))
	for _, m := range in {
		out = append(out, redact.MountInput{
			Type:   m.Type,
			Source: m.Source,
			Target: m.Target,
			Mode:   m.Mode,
		})
	}
	return out
}

func sortedRuntimes(in []ServiceRuntime) []ServiceRuntime {
	out := append([]ServiceRuntime(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}
