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
)

// Client is a read-only Docker Engine client.
type Client struct {
	api *client.Client
}

// New connects to the Docker API at host.
func New(host string) (*Client, error) {
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
	return &Client{api: api}, nil
}

// Close releases the underlying transport.
func (c *Client) Close() error { return c.api.Close() }

// Version pings the engine and returns its reported version.
func (c *Client) Version(ctx context.Context) (string, error) {
	v, err := c.api.ServerVersion(ctx)
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
	list, err := c.api.ContainerList(ctx, container.ListOptions{
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
