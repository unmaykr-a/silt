// Package demo builds a populated Silt database without a Docker host.
//
// It exists because every round of UI work needs realistic data — a screen
// with one healthy project proves nothing about a screen designed for forty —
// and because the alternative was writing a throwaway seeder each time and
// deleting it afterwards. The end-to-end suite needs the same data, so it is
// no longer throwaway.
//
// Nothing here ships: the image builds ./cmd/silt only.
package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/docker"
	"github.com/unmaykr-a/silt/internal/redact"
	"github.com/unmaykr-a/silt/internal/store"
)

// Service is one container in the demo host.
type Service struct {
	Name     string
	Image    string
	State    string
	Health   string
	Restarts int
	ExitCode *int
	OOMKille bool
	// StartedAgo is how long ago the container last started. For a container
	// with restarts that is when it last restarted, which is what decides
	// whether those restarts still count as recent.
	StartedAgo time.Duration
}

// Stack is one Compose project.
type Stack struct {
	Name     string
	Services []Service
	// File, when set, is captured as the project's compose file so the diff
	// and drift screens have something to show.
	File string
}

func ptr(v int) *int { return &v }

// Stacks is the demo host: every container state Silt can render, a project
// large enough to test truncation, and a spread of ages.
//
// Deliberately shaped like a real homelab rather than a matrix of cases — the
// point is to catch a layout that only breaks with fourteen projects, or a
// badge that only looks wrong beside three others.
func Stacks() []Stack {
	ok := func(name, image string) Service {
		return Service{Name: name, Image: image, State: "running", Health: "healthy"}
	}
	fileV1 := "services:\n  app:\n    image: app:1.0\n    restart: unless-stopped\n    ports:\n      - 8080:80\n"

	return []Stack{
		{Name: "media", File: fileV1, Services: []Service{
			ok("radarr", "lscr.io/linuxserver/radarr:5.4.0"),
			ok("sonarr", "lscr.io/linuxserver/sonarr:4.0.1"),
			ok("prowlarr", "lscr.io/linuxserver/prowlarr:1.14"),
			ok("bazarr", "lscr.io/linuxserver/bazarr:1.4"),
		}},
		{Name: "monitoring", File: fileV1, Services: []Service{
			ok("grafana", "grafana/grafana:11.2.0"),
			ok("prometheus", "prom/prometheus:v2.54"),
			ok("loki", "grafana/loki:3.1"),
		}},
		// No healthcheck at all, which is not the same as healthy.
		{Name: "dns", Services: []Service{{Name: "pihole", Image: "pihole/pihole:2024.07", State: "running"}}},
		// Running and answering wrongly.
		{Name: "immich", File: fileV1, Services: []Service{
			{Name: "server", Image: "ghcr.io/immich-app/immich-server:v1.115",
				State: "running", Health: "unhealthy", Restarts: 12, StartedAgo: 2 * time.Hour},
			ok("redis", "redis:7"),
			ok("database", "tensorchord/pgvecto-rs:pg16"),
		}},
		// Killed by the kernel for memory.
		{Name: "nextcloud", File: fileV1, Services: []Service{
			{Name: "app", Image: "nextcloud:29-apache", State: "exited", ExitCode: ptr(137), OOMKille: true},
			ok("db", "postgres:16"),
		}},
		// Stopped with a code nobody asked for.
		{Name: "paperless", Services: []Service{
			ok("webserver", "ghcr.io/paperless-ngx/paperless-ngx:2.11"),
			{Name: "gotenberg", Image: "gotenberg/gotenberg:8", State: "exited", ExitCode: ptr(1)},
			ok("broker", "redis:7"),
		}},
		// Stopped on purpose: a state, not a fault.
		{Name: "backup", Services: []Service{
			{Name: "restic", Image: "restic/restic:0.17", State: "exited", ExitCode: ptr(0)},
		}},
		// In a loop.
		{Name: "homeassistant", Services: []Service{
			{Name: "homeassistant", Image: "ghcr.io/home-assistant/home-assistant:2024.9",
				State: "restarting", Restarts: 8, StartedAgo: 3 * time.Hour},
		}},
		{Name: "sandbox", Services: []Service{
			{Name: "worker", Image: "alpine:3.20", State: "paused"},
			{Name: "api", Image: "alpine:3.20", State: "running", Health: "starting"},
		}},
		{Name: "gitea", File: fileV1, Services: []Service{ok("server", "gitea/gitea:1.22")}},
		{Name: "vaultwarden", Services: []Service{ok("vaultwarden", "vaultwarden/server:1.32")}},
		// Restarted recently: still amber.
		{Name: "jellyfin", Services: []Service{
			{Name: "jellyfin", Image: "jellyfin/jellyfin:10.9",
				State: "running", Health: "healthy", Restarts: 4, StartedAgo: time.Hour},
		}},
		// Restarted long ago and stable since: grey, and not an alarm.
		{Name: "syncthing", Services: []Service{
			{Name: "syncthing", Image: "syncthing/syncthing:1.27",
				State: "running", Health: "healthy", Restarts: 6, StartedAgo: 10 * 24 * time.Hour},
		}},
		// Long enough to test truncation everywhere a name is shown.
		{Name: "a-very-long-project-name-that-should-truncate", Services: []Service{
			ok("svc", "alpine:3.20"),
		}},
	}
}

type identity struct{ name, dir string }

func (i identity) ProjectName() string       { return i.name }
func (i identity) ProjectWorkingDir() string { return i.dir }
func (i identity) ConfigFiles() []string     { return []string{i.dir + "/compose.yaml"} }

// Seed writes the demo host into db, with enough history for the graphs to
// have shape and one project carrying an unapplied compose edit.
func Seed(ctx context.Context, db *store.Store) error {
	key, err := db.RedactionKey(ctx)
	if err != nil {
		return fmt.Errorf("redaction key: %w", err)
	}
	r := redact.New(key, nil)

	now := store.Now()
	const hour = int64(time.Hour / time.Millisecond)

	for _, stack := range Stacks() {
		dir := "/srv/" + stack.Name
		_, projectID, err := db.UpsertHostAndProject(ctx, "local",
			"tcp://docker-socket-proxy:2375", "28.0", identity{stack.Name, dir})
		if err != nil {
			return fmt.Errorf("upsert %s: %w", stack.Name, err)
		}

		// Seven observations over two days, with the image changing twice, so
		// the density strip and the image history are not a single point.
		for i := 6; i >= 0; i-- {
			when := now - int64(i)*8*hour
			services := append([]Service(nil), stack.Services...)
			if i%3 == 0 && len(services) > 0 {
				services[0].Image += "-alt"
			}
			obs, err := build(r, stack.Name, dir, services, stack.File, when)
			if err != nil {
				return err
			}
			if _, err := db.WriteSnapshot(ctx, projectID, when, "interval", obs); err != nil {
				return fmt.Errorf("snapshot %s: %w", stack.Name, err)
			}
		}

		// gitea gets an edit nobody applied: the same running configuration,
		// a different file on disk. That is drift, and it is the one state
		// that cannot be produced by changing a container.
		if stack.Name == "gitea" {
			edited := "services:\n  server:\n    image: gitea/gitea:1.22\n    restart: always\n    ports:\n      - 3000:3000\n      - 2222:22\n"
			services := append([]Service(nil), stack.Services...)
			services[0].Image += "-alt" // match the last snapshot, so only the file differs
			obs, err := build(r, stack.Name, dir, services, edited, now)
			if err != nil {
				return err
			}
			if _, err := db.WriteSnapshot(ctx, projectID, now+1000, "file", obs); err != nil {
				return fmt.Errorf("drift snapshot: %w", err)
			}
		}

		if err := seedEvents(ctx, db, projectID, stack, now, hour); err != nil {
			return err
		}
	}
	return nil
}

func seedEvents(ctx context.Context, db *store.Store, projectID int64, stack Stack, now, hour int64) error {
	type ev struct{ service, typ, severity, message string }
	var events []ev

	switch stack.Name {
	case "immich":
		events = []ev{
			{"server", "container.die", store.SeverityError, "container died with code 137"},
			{"server", "monitor.down", store.SeverityError, "immich probe failed"},
		}
	case "homeassistant":
		events = []ev{{"homeassistant", "container.restart", store.SeverityWarn, "restarting"}}
	case "nextcloud":
		events = []ev{{"app", "container.oom", store.SeverityError, "out of memory"}}
	case "media":
		events = []ev{{"radarr", "image.pull", store.SeverityInfo, "pulled a new image"}}
	case "gitea":
		events = []ev{{"", "config.drift", store.SeverityWarn, "compose file changed but the running stack has not"}}
	default:
		return nil
	}

	for i, e := range events {
		id := projectID
		if _, err := db.RecordEvent(ctx, store.EventRecord{
			ProjectID: &id, Service: e.service, TS: now - int64(i)*hour,
			Source: store.SourceDocker, Type: e.typ, Severity: e.severity, Message: e.message,
			Payload: map[string]any{"project": stack.Name},
		}); err != nil {
			return fmt.Errorf("record event: %w", err)
		}
	}
	return nil
}

func build(r *redact.Redactor, name, dir string, services []Service, file string, when int64) (compose.Observation, error) {
	inputs := make([]compose.ServiceInput, 0, len(services))
	for _, s := range services {
		started := when - int64(30*24*time.Hour/time.Millisecond)
		if s.StartedAgo > 0 {
			started = when - int64(s.StartedAgo/time.Millisecond)
		}
		inputs = append(inputs, compose.ServiceInput{
			Service: s.Name,
			Inspected: docker.Inspected{
				Config: docker.ContainerConfig{
					Image:   s.Image,
					ImageID: "sha256:" + name + s.Name,
					// A secret and two keep-list values, so the redaction is
					// visible on the service screen rather than theoretical.
					Env: []string{"PUID=1000", "TZ=Europe/Tallinn", "API_KEY=demo-secret-" + s.Name},
				},
				Runtime: docker.RuntimeState{
					ContainerID:   name + "-" + s.Name,
					ContainerName: "/" + name + "-" + s.Name + "-1",
					State:         s.State,
					Health:        s.Health,
					RestartCount:  s.Restarts,
					StartedAt:     &started,
					ExitCode:      s.ExitCode,
					OOMKilled:     s.OOMKille,
				},
			},
		})
	}

	obs, err := compose.Build(
		docker.Project{Name: name, WorkingDir: dir, ConfigFiles: []string{dir + "/compose.yaml"}},
		inputs, r)
	if err != nil {
		return compose.Observation{}, fmt.Errorf("build %s: %w", name, err)
	}
	if file != "" {
		obs.Files = []compose.CapturedFile{{
			Path: dir + "/compose.yaml", Status: compose.FileOK,
			Content: []byte(file), LineCount: 6, Size: int64(len(file)),
		}}
		obs.Project.Source = compose.SourceFiles
	}
	return obs, nil
}
