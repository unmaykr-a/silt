package collect

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

// Trigger values recorded on a snapshot.
const (
	TriggerEvent    = "event"
	TriggerInterval = "interval"
	TriggerManual   = "manual"
)

// projectIdentity adapts docker.Project to what the store needs, so the store
// does not import the docker package.
type projectIdentity struct{ p docker.Project }

func (a projectIdentity) ProjectName() string       { return a.p.Name }
func (a projectIdentity) ProjectWorkingDir() string { return a.p.WorkingDir }
func (a projectIdentity) ConfigFiles() []string {
	if a.p.ConfigFiles == nil {
		return []string{}
	}
	return a.p.ConfigFiles
}

// Snapshotter builds and persists snapshots.
type Snapshotter struct {
	Client   *docker.Client
	Store    *store.Store
	Redactor *redact.Redactor
	Log      *slog.Logger
	HostName string
	Endpoint string
}

// Snapshot observes one project and writes the result.
func (s *Snapshotter) Snapshot(ctx context.Context, p docker.Project, trigger string) (store.SnapshotResult, error) {
	dockerVersion, err := s.Client.Version(ctx)
	if err != nil {
		// A snapshot without the engine version is still worth having.
		dockerVersion = ""
	}

	_, projectID, err := s.Store.UpsertHostAndProject(ctx, s.HostName, s.Endpoint, dockerVersion, projectIdentity{p})
	if err != nil {
		return store.SnapshotResult{}, err
	}

	inputs := make([]compose.ServiceInput, 0, len(p.Services))
	for _, svc := range p.Services {
		inspected, err := s.Client.Inspect(ctx, svc.ContainerID)
		if err != nil {
			// A container can vanish between listing and inspecting, which is
			// routine during `compose up`. Skip it rather than losing the
			// whole project's snapshot.
			s.Log.Debug("skipping container", "service", svc.Name, "error", err)
			continue
		}

		var digest string
		var created int64
		if ref := inspected.Config.Image; ref != "" {
			if _, d, c, err := s.Client.ImageIdentity(ctx, ref); err == nil {
				digest, created = d, c
			} else {
				s.Log.Debug("image identity unavailable", "image", ref, "error", err)
			}
		}

		inputs = append(inputs, compose.ServiceInput{
			Service:      svc.Name,
			Inspected:    inspected,
			ImageDigest:  digest,
			ImageCreated: created,
		})
	}

	obs, err := compose.Build(p, inputs, s.Redactor)
	if err != nil {
		return store.SnapshotResult{}, fmt.Errorf("build model for %s: %w", p.Name, err)
	}

	result, err := s.Store.WriteSnapshot(ctx, projectID, store.Now(), trigger, obs)
	if err != nil {
		return store.SnapshotResult{}, fmt.Errorf("write snapshot for %s: %w", p.Name, err)
	}
	return result, nil
}

// SnapshotAll observes every discovered project.
func (s *Snapshotter) SnapshotAll(ctx context.Context, trigger string) error {
	projects, err := s.Client.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover projects: %w", err)
	}
	for _, p := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, err := s.Snapshot(ctx, p, trigger)
		if err != nil {
			s.Log.Error("snapshot failed", "project", p.Name, "error", err)
			continue
		}
		s.logResult(p.Name, trigger, result)
	}
	return nil
}

func (s *Snapshotter) logResult(project, trigger string, r store.SnapshotResult) {
	switch {
	case r.ConfigChanged:
		s.Log.Info("configuration changed", "project", project, "snapshot", r.ID, "trigger", trigger)
	case r.RuntimeChanged:
		s.Log.Info("runtime changed", "project", project, "snapshot", r.ID, "trigger", trigger)
	default:
		s.Log.Debug("no change", "project", project, "snapshot", r.ID, "trigger", trigger)
	}
}
