// Package docker talks to the Docker Engine API, read-only.
//
// Silt never writes to the Docker API. The documented deployment puts a
// socket proxy with POST=0 in front of the engine so that rule is enforced
// outside this process as well as inside it. See PROJECT.md Section 3.
package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// Compose writes these labels onto every container it creates. They are the
// whole of Silt's discovery mechanism: no user-configured paths, no guessing.
// See PROJECT.md Section 5.
const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelConfigHash  = "com.docker.compose.config-hash"
	LabelDependsOn   = "com.docker.compose.depends_on"
)

// Client is a read-only Docker Engine client.
//
// The endpoint behind it can change while Silt runs, so callers hold the
// Client rather than the transport and read it through engine() under a read
// lock. That is what lets SILT_DOCKER_HOST be a setting rather than a restart:
// everything that observes this host — the collector, the snapshotter, the
// settings screen's probe — holds this one pointer.
type Client struct {
	mu   sync.RWMutex
	host string
	api  *client.Client
}

// New connects to the Docker API at host.
func New(host string) (*Client, error) {
	api, err := dial(host)
	if err != nil {
		return nil, err
	}
	return &Client{host: host, api: api}, nil
}

func dial(host string) (*client.Client, error) {
	api, err := client.NewClientWithOpts(
		client.WithHost(host),
		// The engine's API version may be older than the client's. Negotiation
		// needs /version and /_ping to be reachable, which is why the socket
		// proxy must set VERSION=1 and PING=1.
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client for %q: %w", host, err)
	}
	return api, nil
}

// engine returns the current transport.
func (c *Client) engine() *client.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.api
}

// Host is the endpoint currently dialled.
func (c *Client) Host() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.host
}

// Redial points the client at a different endpoint, reporting whether anything
// changed.
//
// The old transport is closed, which is deliberate rather than tidy: the event
// watcher holds a long-lived stream against it, and closing it is what ends
// that stream so the watcher's own reconnect loop picks up the new endpoint. A
// watcher that kept streaming from the old engine would leave Silt recording
// one host while claiming to watch another.
//
// A dial failure leaves the old endpoint in place, because a typo in the
// settings screen should not take the collector down with it.
func (c *Client) Redial(host string) (bool, error) {
	c.mu.Lock()
	if host == c.host {
		c.mu.Unlock()
		return false, nil
	}
	api, err := dial(host)
	if err != nil {
		c.mu.Unlock()
		return false, err
	}
	previous := c.api
	c.api, c.host = api, host
	c.mu.Unlock()

	if previous != nil {
		_ = previous.Close()
	}
	return true, nil
}

// Close releases the underlying transport.
func (c *Client) Close() error { return c.engine().Close() }

// Version pings the engine and returns its reported version.
func (c *Client) Version(ctx context.Context) (string, error) {
	v, err := c.engine().ServerVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("docker version: %w", err)
	}
	return v.Version, nil
}

// Service is one Compose service as currently realised by a container.
type Service struct {
	Name          string
	ContainerID   string
	ContainerName string
	Image         string
	State         string
	Status        string
	ConfigHash    string
}

// Project is a Compose project discovered from container labels.
type Project struct {
	Name        string
	WorkingDir  string
	ConfigFiles []string
	Services    []Service
}

// Discover enumerates every container carrying a Compose project label and
// groups them into projects. Stopped containers are included: a service that
// died is exactly the thing Silt exists to notice.
func (c *Client) Discover(ctx context.Context) ([]Project, error) {
	list, err := c.engine().ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", LabelProject)),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return groupByProject(list), nil
}

// groupByProject is split out from Discover so it can be tested against
// hand-built container summaries without an engine.
func groupByProject(list []container.Summary) []Project {
	byName := make(map[string]*Project)
	for _, ctr := range list {
		name := ctr.Labels[LabelProject]
		if name == "" {
			continue
		}
		p, ok := byName[name]
		if !ok {
			p = &Project{Name: name}
			byName[name] = p
		}
		// Compose writes the same project-level labels onto every container in
		// the project, so the first non-empty value wins and later containers
		// only fill gaps.
		if p.WorkingDir == "" {
			p.WorkingDir = ctr.Labels[LabelWorkingDir]
		}
		if len(p.ConfigFiles) == 0 {
			p.ConfigFiles = splitConfigFiles(ctr.Labels[LabelConfigFiles])
		}
		p.Services = append(p.Services, Service{
			Name:          ctr.Labels[LabelService],
			ContainerID:   ctr.ID,
			ContainerName: containerName(ctr),
			Image:         ctr.Image,
			State:         ctr.State,
			Status:        ctr.Status,
			ConfigHash:    ctr.Labels[LabelConfigHash],
		})
	}

	projects := make([]Project, 0, len(byName))
	for _, p := range byName {
		// Deterministic order: Docker returns containers newest-first, which
		// would make otherwise-identical observations look different.
		sort.Slice(p.Services, func(i, j int) bool {
			if p.Services[i].Name != p.Services[j].Name {
				return p.Services[i].Name < p.Services[j].Name
			}
			return p.Services[i].ContainerID < p.Services[j].ContainerID
		})
		projects = append(projects, *p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects
}

// splitConfigFiles parses the comma-separated config_files label.
func splitConfigFiles(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containerName(ctr container.Summary) string {
	if len(ctr.Names) == 0 {
		return ""
	}
	// The API returns names with a leading slash.
	return strings.TrimPrefix(ctr.Names[0], "/")
}

// Event is a normalised Docker event.
type Event struct {
	Type    string
	Action  string
	Project string
	Service string
	ActorID string
	Image   string
	At      time.Time
}
