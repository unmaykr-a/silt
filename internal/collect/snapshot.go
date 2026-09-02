package collect

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/diff"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/notify"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
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

// Publisher receives things worth telling connected clients about. The
// collector does not depend on the API package for this.
type Publisher interface {
	PublishEvent(payload any)
	PublishChange(payload any)
}

// Snapshotter builds and persists snapshots.
type Snapshotter struct {
	Client   *docker.Client
	Store    *store.Store
	Redactor *redact.Redactor
	Log      *slog.Logger
	HostName string
	Endpoint string
	// Publisher is optional; nil means nothing is broadcast.
	Publisher Publisher
	// Notifier is optional; a nil one is a working no-op.
	Notifier Notifier
	// BaseURL is used to build links in notifications.
	BaseURL string
	// BaseURLFn, when set, supersedes BaseURL. It is editable on the settings
	// screen, and a link in a notification is worth getting right without a
	// restart.
	BaseURLFn func() string
	// Files captures compose files from disk. Nil or disabled means Silt
	// records only what is running, which needs no mounts.
	Files *compose.FileReader
}

// SnapshotProject snapshots one project by its database id, for
// POST /api/projects/{id}/snapshot.
func (s *Snapshotter) SnapshotProject(ctx context.Context, projectID int64) error {
	project, err := s.Store.RQ.GetProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read project %d: %w", projectID, err)
	}
	discovered, err := s.Client.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover projects: %w", err)
	}
	for _, p := range discovered {
		if p.Name != project.Name {
			continue
		}
		result, err := s.Snapshot(ctx, p, TriggerManual)
		if err != nil {
			return err
		}
		s.logResult(p.Name, TriggerManual, result)
		return nil
	}
	return fmt.Errorf("project %q has no running containers", project.Name)
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

	if s.Files.Enabled() && len(p.ConfigFiles) > 0 {
		rules, err := s.Store.RedactionRules(ctx, projectID)
		if err != nil {
			s.Log.Error("read redaction rules", "project", p.Name, "error", err)
		}
		obs.Files = s.Files.Capture(p.ConfigFiles, rules)
		if len(obs.Files) > 0 {
			obs.Project.Source = compose.SourceFiles
		}
	}

	result, err := s.Store.WriteSnapshot(ctx, projectID, store.Now(), trigger, obs)
	if err != nil {
		return store.SnapshotResult{}, fmt.Errorf("write snapshot for %s: %w", p.Name, err)
	}

	// A configuration change is the thing Silt exists to record, so it earns
	// an event row of its own and a notification.
	if result.ConfigChanged {
		payload := map[string]any{
			"snapshot_id": result.ID,
			"project_id":  projectID,
			"project":     p.Name,
			"trigger":     trigger,
			"taken_at":    result.TakenAt,
		}
		id := projectID
		if _, err := s.Store.RecordEvent(ctx, store.EventRecord{
			ProjectID: &id,
			TS:        result.TakenAt,
			Source:    store.SourceSilt,
			Type:      "snapshot.changed",
			Severity:  store.SeverityInfo,
			Message:   "configuration changed",
			Payload:   payload,
		}); err != nil {
			s.Log.Error("record snapshot event", "project", p.Name, "error", err)
		}
		s.notifyChange(ctx, projectID, p.Name, result)
	}

	// Broadcast whenever a snapshot was actually written, not only when the
	// configuration changed.
	//
	// This used to fire on ConfigChanged alone, on the reasoning that a
	// runtime change is "already covered by the docker events that caused it".
	// That held while the UI only showed configuration. It stopped holding
	// when the project screens started showing runtime state — running counts,
	// unhealthy, restarting — and it was wrong in two ways at once.
	//
	// Ordering: the docker event is broadcast the moment it arrives, but the
	// snapshot it triggers is written after the coalescing window. A browser
	// refetching on that event reads the state from *before* the change, and
	// nothing ever told it to look again. The reported symptom was seeing an
	// update on a project only after a reload.
	//
	// Coverage: the interval sweep produces no docker event at all, so a
	// health flip or a restart found by the sweep was invisible until reload.
	//
	// A touched snapshot is deliberately silent: nothing changed, so there is
	// nothing to refetch, and on an idle host of forty projects that would be
	// a broadcast per project per interval saying so.
	if s.Publisher != nil && shouldBroadcast(result) {
		s.Publisher.PublishChange(map[string]any{
			"snapshot_id":     result.ID,
			"project_id":      projectID,
			"project":         p.Name,
			"trigger":         trigger,
			"taken_at":        result.TakenAt,
			"config_changed":  result.ConfigChanged,
			"runtime_changed": result.RuntimeChanged,
			"files_changed":   result.FilesChanged,
		})
	}

	// A file that changed without the running configuration changing is
	// drift: someone edited the compose file and has not applied it. That is
	// worth surfacing precisely because nothing broke yet.
	if result.FilesChanged && !result.ConfigChanged && !result.Touched {
		id := projectID
		if _, err := s.Store.RecordEvent(ctx, store.EventRecord{
			ProjectID: &id,
			TS:        result.TakenAt,
			Source:    store.SourceSilt,
			Type:      "config.drift",
			Severity:  store.SeverityWarn,
			Message:   "compose file changed but the running stack has not",
			Payload:   map[string]any{"project": p.Name, "snapshot_id": result.ID},
		}); err != nil {
			s.Log.Error("record drift event", "project", p.Name, "error", err)
		}
		if s.Publisher != nil {
			s.Publisher.PublishEvent(map[string]any{
				"type": "config.drift", "severity": store.SeverityWarn,
				"project": p.Name, "ts": result.TakenAt,
				"message": "compose file changed but the running stack has not",
			})
		}
	}
	return result, nil
}

// notifyChange diffs the new snapshot against the previous configuration
// change and sends anything that passes the filter.
//
// It compares against the previous CHANGED snapshot rather than the previous
// snapshot: runtime-only rows sit in between, and diffing against one of those
// would report the same configuration change again.
func (s *Snapshotter) notifyChange(ctx context.Context, projectID int64, project string, result store.SnapshotResult) {
	if s.Notifier == nil || !s.Notifier.Enabled() {
		return
	}

	previous, err := s.Store.RQ.LatestChangedSnapshotsBefore(ctx, sqlcgen.LatestChangedSnapshotsBeforeParams{
		ProjectID: projectID,
		Before:    result.TakenAt,
		MaxRows:   1,
	})
	if err != nil || len(previous) == 0 {
		// The first configuration change for a project has nothing to compare
		// against; announcing "everything is new" would be noise.
		return
	}

	from, err := s.Store.LoadSnapshotModel(ctx, previous[0].ID)
	if err != nil {
		s.Log.Error("load previous snapshot for notification", "project", project, "error", err)
		return
	}
	to, err := s.Store.LoadSnapshotModel(ctx, result.ID)
	if err != nil {
		s.Log.Error("load snapshot for notification", "project", project, "error", err)
		return
	}

	computed := diff.Compute(toDiffInput(from), toDiffInput(to))
	s.Notifier.Notify(ctx, notify.Change{
		Project:    project,
		SnapshotID: result.ID,
		FromID:     previous[0].ID,
		Changes:    computed.Changes,
		BaseURL:    s.baseURL(),
	})
}

// toDiffInput adapts a stored snapshot for the diff engine.
func toDiffInput(m store.SnapshotModel) diff.Input {
	runtimes := make(map[string]diff.Runtime, len(m.Runtimes))
	for name, rt := range m.Runtimes {
		runtimes[name] = diff.Runtime{
			State:        rt.State,
			Health:       rt.Health,
			RestartCount: rt.RestartCount,
		}
	}
	return diff.Input{
		Side:     diff.Side{SnapshotID: m.Snapshot.ID, TakenAt: m.Snapshot.TakenAt},
		Project:  m.Project,
		Runtimes: runtimes,
	}
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

// Notifier is what the snapshotter needs from the notification layer.
type Notifier interface {
	Notify(ctx context.Context, c notify.Change)
	// Enabled lets the snapshotter skip loading and diffing two snapshots when
	// there is nowhere to send the result.
	Enabled() bool
}

// baseURL is the link prefix in force right now.
func (s *Snapshotter) baseURL() string {
	if s.BaseURLFn != nil {
		return s.BaseURLFn()
	}
	return s.BaseURL
}

// shouldBroadcast reports whether a written snapshot is worth telling connected
// clients about.
//
// A named rule rather than a condition inline, because getting it wrong is
// invisible: the UI simply shows yesterday's state and nobody notices until
// they reload. See the call site for the two ways the old ConfigChanged-only
// version was wrong.
func shouldBroadcast(r store.SnapshotResult) bool {
	// Touched means the observation matched the previous snapshot exactly.
	// Nothing changed, so there is nothing to refetch — and on an idle host of
	// forty projects, broadcasting it would be one message per project per
	// interval to say so.
	if r.Touched {
		return false
	}
	return r.ConfigChanged || r.RuntimeChanged || r.FilesChanged
}
