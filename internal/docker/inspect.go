package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Inspected is the normalised, allowlisted subset of `docker inspect` that
// Silt records for one container.
//
// The split between Config and Runtime is load-bearing. The config
// fingerprint hashes Config only; if a volatile field leaked into it, a
// container restarting would register as a configuration change and the whole
// two-fingerprint design would be defeated at source. That is why this is an
// explicit allowlist of fields rather than a denylist over the raw inspect.
type Inspected struct {
	Config  ContainerConfig
	Runtime RuntimeState
}

// ContainerConfig is the configuration half: stable while the container is
// unchanged, whatever it is doing.
type ContainerConfig struct {
	Image         string            `json:"image"`
	ImageID       string            `json:"image_id"`
	Env           []string          `json:"env,omitempty"`
	Cmd           []string          `json:"cmd,omitempty"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty"`
	User          string            `json:"user,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	ExposedPorts  []string          `json:"exposed_ports,omitempty"`
	PortBindings  []string          `json:"port_bindings,omitempty"`
	Mounts        []Mount           `json:"mounts,omitempty"`
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

// Mount is one mount, kept structured so redaction can treat the host source
// path differently from the container-side target.
type Mount struct {
	Type   string `json:"type"`
	Source string `json:"source,omitempty"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
}

// RuntimeState is the volatile half: never part of the config fingerprint.
type RuntimeState struct {
	ContainerID   string
	ContainerName string
	State         string
	Health        string
	RestartCount  int
	StartedAt     *int64 // unix ms

	// ExitCode is why a container is not running, and is the difference
	// between "you stopped this" and "this died". Docker reports it for a
	// running container too, as the code of the previous run, so it is only
	// meaningful once State is exited or dead — the store records it as NULL
	// otherwise rather than storing a number that means nothing.
	ExitCode *int
	// OOMKilled distinguishes the most common cause of a 137 from a plain
	// SIGKILL, which are the same exit code and very different problems.
	OOMKilled bool
}

// Inspect reads one container and returns the normalised subset.
func (c *Client) Inspect(ctx context.Context, id string) (Inspected, error) {
	raw, err := c.api.ContainerInspect(ctx, id)
	if err != nil {
		return Inspected{}, fmt.Errorf("inspect container %s: %w", short(id), err)
	}
	return normaliseInspect(raw), nil
}

// ImageIdentity resolves an image reference to its local ID and, when the
// image came from a registry, its digest.
//
// The local ID is always present and is what the fingerprint uses.
// RepoDigests is empty for locally-built images and can hold several entries
// across registries, so match on repository rather than taking the first.
func (c *Client) ImageIdentity(ctx context.Context, ref string) (id, digest string, created int64, err error) {
	img, err := c.api.ImageInspect(ctx, ref)
	if err != nil {
		return "", "", 0, fmt.Errorf("inspect image %s: %w", ref, err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, img.Created); perr == nil {
		created = t.UnixMilli()
	}
	return img.ID, pickRepoDigest(ref, img.RepoDigests), created, nil
}

// pickRepoDigest chooses the digest whose repository matches ref.
func pickRepoDigest(ref string, repoDigests []string) string {
	repo := repoOf(ref)
	for _, rd := range repoDigests {
		if repoOf(rd) == repo {
			return digestOf(rd)
		}
	}
	// No repository match. A single entry is still better than nothing; more
	// than one and there is no way to tell which registry this came from, so
	// report none rather than guess.
	if len(repoDigests) == 1 {
		return digestOf(repoDigests[0])
	}
	return ""
}

func repoOf(ref string) string {
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	// Strip a tag, taking care not to mistake a registry port for one.
	if i := strings.LastIndexByte(ref, ':'); i >= 0 && !strings.Contains(ref[i+1:], "/") {
		ref = ref[:i]
	}
	return ref
}

func digestOf(repoDigest string) string {
	if i := strings.IndexByte(repoDigest, '@'); i >= 0 {
		return repoDigest[i+1:]
	}
	return ""
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// normaliseInspect flattens the raw inspect into deterministic, sorted form.
//
// Sorting is not cosmetic. Docker returns maps in arbitrary order, and
// unsorted output makes every observation differ from the last, which turns
// Silt from a change journal into a noise generator.
func normaliseInspect(raw container.InspectResponse) Inspected {
	out := Inspected{}

	if raw.Config != nil {
		out.Config.Image = raw.Config.Image
		out.Config.Env = sortedCopy(raw.Config.Env)
		out.Config.Cmd = append([]string(nil), raw.Config.Cmd...)
		out.Config.Entrypoint = append([]string(nil), raw.Config.Entrypoint...)
		out.Config.WorkingDir = raw.Config.WorkingDir
		out.Config.User = raw.Config.User
		out.Config.Labels = raw.Config.Labels
		for p := range raw.Config.ExposedPorts {
			out.Config.ExposedPorts = append(out.Config.ExposedPorts, string(p))
		}
		sort.Strings(out.Config.ExposedPorts)
		if raw.Config.Healthcheck != nil {
			out.Config.Healthcheck = append([]string(nil), raw.Config.Healthcheck.Test...)
		}
		out.Config.DependsOn = parseDependsOn(raw.Config.Labels[LabelDependsOn])
	}
	out.Config.ImageID = raw.Image

	if raw.HostConfig != nil {
		for port, bindings := range raw.HostConfig.PortBindings {
			for _, b := range bindings {
				out.Config.PortBindings = append(out.Config.PortBindings,
					strings.TrimSuffix(b.HostIP+":"+b.HostPort, ":")+"->"+string(port))
			}
		}
		sort.Strings(out.Config.PortBindings)
		out.Config.RestartPolicy = string(raw.HostConfig.RestartPolicy.Name)
		out.Config.Privileged = raw.HostConfig.Privileged
		out.Config.CapAdd = sortedCopy(raw.HostConfig.CapAdd)
		out.Config.CapDrop = sortedCopy(raw.HostConfig.CapDrop)
		out.Config.MemoryLimit = raw.HostConfig.Memory
		out.Config.NanoCPUs = raw.HostConfig.NanoCPUs
	}

	for _, m := range raw.Mounts {
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		}
		mode := "rw"
		if !m.RW {
			mode = "ro"
		}
		out.Config.Mounts = append(out.Config.Mounts, Mount{
			Type:   string(m.Type),
			Source: src,
			Target: m.Destination,
			Mode:   mode,
		})
	}
	sort.Slice(out.Config.Mounts, func(i, j int) bool {
		a, b := out.Config.Mounts[i], out.Config.Mounts[j]
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Source < b.Source
	})

	if raw.NetworkSettings != nil {
		for name := range raw.NetworkSettings.Networks {
			out.Config.Networks = append(out.Config.Networks, name)
		}
		sort.Strings(out.Config.Networks)
	}

	out.Runtime.ContainerID = raw.ID
	out.Runtime.ContainerName = strings.TrimPrefix(raw.Name, "/")
	if raw.State != nil {
		out.Runtime.State = raw.State.Status
		out.Runtime.RestartCount = raw.RestartCount
		if raw.State.Health != nil {
			out.Runtime.Health = raw.State.Health.Status
		}
		// Only for a container that has actually stopped. While one is
		// running, Docker still reports the previous run's exit code, and
		// showing that as the current state is worse than showing nothing.
		if raw.State.Status == "exited" || raw.State.Status == "dead" {
			code := raw.State.ExitCode
			out.Runtime.ExitCode = &code
			out.Runtime.OOMKilled = raw.State.OOMKilled
		}
		if t, err := time.Parse(time.RFC3339Nano, raw.State.StartedAt); err == nil && !t.IsZero() {
			ms := t.UnixMilli()
			out.Runtime.StartedAt = &ms
		}
	}
	return out
}

// parseDependsOn reads Compose's depends_on label, which is a comma-separated
// list of "service:condition:required" triples. Only the service names are
// interesting for change detection.
func parseDependsOn(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if i := strings.IndexByte(entry, ':'); i >= 0 {
			entry = entry[:i]
		}
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
