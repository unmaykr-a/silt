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
	"hash/fnv"
	"strconv"
	"strings"
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

	// The configuration a diff can move. Empty means the defaults in build():
	// most services in a homelab are three environment variables and a port,
	// and spelling that out fourteen times would bury the ones that differ.
	Env    []string
	Ports  []string
	Mounts []docker.Mount
	Labels map[string]string
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
	// Changes is what happened to this stack over the history, oldest first.
	// Empty means the default in Seed: one image bump, so every stack has a
	// shape on the density strip.
	Changes []Change
}

// Change is one edit to a stack, landing at one point in its history.
//
// The history is replayed forward and changes accumulate, so a diff between
// two adjacent snapshots shows exactly the change that happened between them.
// That is the whole point of the seed: a demo where every diff is a version
// string moving by one digit demonstrates nothing about a screen built to
// group changes by service, kind and severity.
type Change struct {
	// Note says what this change is, for whoever reads the seed next.
	Note string
	// Apply edits the service list in place. It is given a copy.
	Apply func([]Service)
	// File, when set, replaces the captured compose file from here on, so the
	// file diff moves with the configuration rather than staying frozen.
	File string
}

func ptr(v int) *int { return &v }

// The compose file for the media stack, and the three revisions it goes
// through. The file moves with the configuration, so the file diff and the
// structured diff describe the same event rather than disagreeing about it —
// and one of the changed lines is a secret, which is the only way to show that
// a rotated credential is a visibly changed line and nothing more.
const (
	mediaFileV1 = `services:
  radarr:
    image: lscr.io/linuxserver/radarr:5.4.0
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Europe/Tallinn
      - RADARR__API_KEY=8f14e45fceea167a
    ports:
      - 7878:7878
    volumes:
      - /srv/media/radarr:/config
      - /mnt/tank/films:/films
`

	mediaFileV2 = `services:
  radarr:
    image: lscr.io/linuxserver/radarr:5.4.0
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Europe/Tallinn
      - RADARR__API_KEY=8f14e45fceea167a
    ports:
      - 7878:7878
      - 9898:9898
    volumes:
      - /srv/media/radarr:/config
      - /mnt/tank/films:/films
      - /mnt/tank/downloads:/downloads
`

	mediaFileV3 = `services:
  radarr:
    image: lscr.io/linuxserver/radarr:5.6.0
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Europe/Tallinn
      - LOG_LEVEL=debug
      - RADARR__API_KEY=c4ca4238a0b92382
    ports:
      - 7878:7878
      - 9898:9898
    volumes:
      - /srv/media/radarr:/config
      - /mnt/tank/films:/films
      - /mnt/tank/downloads:/downloads
`
)

// revisions is how many observations each stack gets. Seven over two days is
// enough for a density strip to have shape without the history becoming a
// scroll.
const revisions = 7

// defaultChange is what a stack with no history of its own does: bump the
// first service's image once, so every project has something on the strip and
// something in its diff list.
func defaultChange() Change {
	return Change{
		Note: "routine image bump",
		Apply: func(services []Service) {
			if len(services) == 0 {
				return
			}
			services[0].Image = bumpTag(services[0].Image)
		},
	}
}

// bumpTag advances the last run of digits in an image tag.
//
// Crude on purpose: it has to produce something that looks like a real
// upgrade, and "5.4.0" -> "5.4.1" does. The previous seed appended "-alt",
// which produced diffs reading `radarr:5.4.0 -> radarr:5.4.0-alt` — visibly
// synthetic, in the screenshot the project leads with.
//
// The digits are the *last run anywhere in the tag*, not the trailing
// characters: "29-apache" ends in letters, and looking only at the end left
// that stack with an unchanging image, seven identical observations, and — via
// touch-instead-of-insert — a single snapshot in its history.
func bumpTag(image string) string {
	colon := strings.LastIndex(image, ":")
	if colon < 0 {
		return image
	}
	tag := image[colon+1:]

	end := len(tag)
	for end > 0 && !isDigit(tag[end-1]) {
		end--
	}
	if end == 0 {
		return image // no digits anywhere to advance
	}
	start := end
	for start > 0 && isDigit(tag[start-1]) {
		start--
	}

	n, err := strconv.Atoi(tag[start:end])
	if err != nil {
		return image
	}
	// Padded to the original width, so "2024.07" advances to "2024.08" rather
	// than to "2024.8".
	bumped := fmt.Sprintf("%0*d", end-start, n+1)
	return image[:colon+1] + tag[:start] + bumped + tag[end:]
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// fakeDigest is a stable 64-hex string for a string, so the demo's image IDs
// and digests look like the real thing and change when the image does.
//
// FNV rather than SHA-256 on purpose: nothing here is a security claim, and a
// real hash in seed data invites someone to check it against a registry.
func fakeDigest(seed string) string {
	var out strings.Builder
	h := fnv.New64a()
	for i := 0; out.Len() < 64; i++ {
		h.Reset()
		fmt.Fprintf(h, "%s/%d", seed, i)
		fmt.Fprintf(&out, "%016x", h.Sum64())
	}
	return out.String()[:64]
}

// giteaChanges is separate because the drift snapshot has to replay to the
// same running state the newest snapshot left behind.
func giteaChanges() []Change {
	return []Change{{
		Note: "gitea upgraded",
		Apply: func(services []Service) {
			services[0].Image = "gitea/gitea:1.23"
		},
	}}
}

// mediaChanges is the history the media stack replays, oldest first.
//
// Ordered so the newest diff — the one "compare last two changes" opens, and
// the one anyone clicking through the demo sees first — is the interesting
// one: an upgrade arriving together with a rotated credential and a new
// setting, which is what a single `compose up` actually looks like.
func mediaChanges() []Change {
	radarr := func(services []Service, edit func(*Service)) {
		for i := range services {
			if services[i].Name == "radarr" {
				edit(&services[i])
			}
		}
	}

	return []Change{
		{
			// One thing moving on its own: the ordinary case, and the one the
			// severity colouring has to get right against the busy one below.
			Note: "sonarr picks up a patch release",
			Apply: func(services []Service) {
				for i := range services {
					if services[i].Name == "sonarr" {
						services[i].Image = "lscr.io/linuxserver/sonarr:4.0.4"
					}
				}
			},
		},
		{
			Note: "a second port, and the downloads share mounted",
			File: mediaFileV2,
			Apply: func(services []Service) {
				radarr(services, func(s *Service) {
					s.Ports = append(append([]string(nil), s.Ports...), "9898:9898/tcp")
					s.Mounts = append(append([]docker.Mount(nil), s.Mounts...),
						docker.Mount{Type: "bind", Source: "/mnt/tank/downloads", Target: "/downloads", Mode: "rw"})
				})
			},
		},
		{
			// The busy one: an upgrade, a rotated key, a new setting. All of
			// it arrives in a single `compose up`, which is why one snapshot
			// has to be able to show more than one change.
			Note: "radarr upgraded, its API key rotated, debug logging turned on",
			File: mediaFileV3,
			Apply: func(services []Service) {
				radarr(services, func(s *Service) {
					s.Image = "lscr.io/linuxserver/radarr:5.6.0"
					s.Env = []string{
						"PUID=1000", "PGID=1000", "TZ=Europe/Tallinn",
						// On the default keep list, so this one stays readable
						// while the key beside it does not. A diff where every
						// value is a digest teaches nothing about which values
						// Silt keeps and which it never stores.
						"LOG_LEVEL=debug",
						"RADARR__API_KEY=demo-secret-rotated",
					}
				})
			},
		},
	}
}

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
		// The stack with a history worth reading. Every other stack shows one
		// change at a time; this one shows what an evening of tinkering
		// actually looks like, which is what the diff screen is built for.
		{Name: "media", File: mediaFileV1, Changes: mediaChanges(), Services: []Service{
			{Name: "radarr", Image: "lscr.io/linuxserver/radarr:5.4.0", State: "running", Health: "healthy",
				Env:   []string{"PUID=1000", "PGID=1000", "TZ=Europe/Tallinn", "RADARR__API_KEY=demo-secret-radarr"},
				Ports: []string{"7878:7878/tcp"},
				Mounts: []docker.Mount{
					{Type: "bind", Source: "/srv/media/radarr", Target: "/config", Mode: "rw"},
					{Type: "bind", Source: "/mnt/tank/films", Target: "/films", Mode: "rw"},
				}},
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
		{Name: "gitea", File: fileV1, Changes: giteaChanges(), Services: []Service{ok("server", "gitea/gitea:1.22")}},
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
func (i identity) ConfigFiles() []string {
	return []string{i.dir + "/compose.yaml", i.dir + "/.env"}
}

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

		// Seven observations over two days, so the density strip and the image
		// history are not a single point.
		//
		// The stack's changes land on the newest revisions, one per step, and
		// accumulate: replayed forward, the diff between any two adjacent
		// snapshots is exactly the change that happened between them.
		changes := stack.Changes
		if len(changes) == 0 {
			changes = []Change{defaultChange()}
		}
		for i := revisions - 1; i >= 0; i-- {
			when := now - int64(i)*8*hour
			services := append([]Service(nil), stack.Services...)
			file := stack.File
			for c := 0; c <= len(changes)-1-i; c++ {
				changes[c].Apply(services)
				if changes[c].File != "" {
					file = changes[c].File
				}
			}
			obs, err := build(r, stack.Name, dir, services, file, when)
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
			edited := "services:\n  server:\n    image: gitea/gitea:1.23\n    restart: always\n    ports:\n      - 3000:3000\n      - 2222:22\n"
			// Replayed to the same state as the newest snapshot, so the running
			// configuration is identical and only the file on disk differs.
			// That is what makes it drift rather than a change.
			services := append([]Service(nil), stack.Services...)
			for _, c := range giteaChanges() {
				c.Apply(services)
			}
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
		// A secret and two keep-list values by default, so redaction is visible
		// on the service screen rather than theoretical. A service that
		// declares its own environment is one whose changes the history moves.
		env := s.Env
		if env == nil {
			env = []string{"PUID=1000", "TZ=Europe/Tallinn", "API_KEY=demo-secret-" + s.Name}
		}
		// Derived from the image reference, so bumping a tag moves the ID and
		// the digest too. Deriving it from the service name instead — which is
		// what this did — meant the image history had exactly one row however
		// many upgrades the history contained, on the screen built to show
		// when an image actually changed.
		inputs = append(inputs, compose.ServiceInput{
			Service:     s.Name,
			ImageDigest: "sha256:" + fakeDigest("digest/"+s.Image),
			Inspected: docker.Inspected{
				Config: docker.ContainerConfig{
					Image:         s.Image,
					ImageID:       "sha256:" + fakeDigest(s.Image),
					Env:           env,
					PortBindings:  s.Ports,
					Mounts:        s.Mounts,
					Labels:        s.Labels,
					RestartPolicy: "unless-stopped",
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
		docker.Project{Name: name, WorkingDir: dir, ConfigFiles: identity{name, dir}.ConfigFiles()},
		inputs, r)
	if err != nil {
		return compose.Observation{}, fmt.Errorf("build %s: %w", name, err)
	}
	if file != "" {
		// Both halves of what a stack is on disk, and both through the same
		// redaction the real capture path uses rather than stored as written.
		// The seed contains a literal API key, and a demo that showed it in
		// cleartext would contradict the one claim the project leads with — on
		// the page anyone evaluating Silt reads first.
		obs.Files = []compose.CapturedFile{
			capture(r, dir+"/compose.yaml", file),
			capture(r, dir+"/.env", envFileFor(name)),
		}
		obs.Project.Source = compose.SourceFiles
	}
	return obs, nil
}

func capture(r *redact.Redactor, path, body string) compose.CapturedFile {
	content, lines := r.ComposeText([]byte(body), compose.NewRuleSet(nil, path))
	return compose.CapturedFile{
		Path: path, Status: compose.FileOK,
		Content: content, Lines: lines, LineCount: len(lines), Size: int64(len(body)),
	}
}

// envFileFor is the .env beside the compose file.
//
// Every stack has one and the demo had none, so the screen built to show
// per-line redaction of a file that is nothing but secrets had only compose
// files to show it on — the easy case, where most lines are structure. It is
// also what makes the file picker a picker rather than a label.
func envFileFor(name string) string {
	return "# Environment for the " + name + " stack.\n" +
		"# Read by docker compose for ${VAR} interpolation.\n" +
		"\n" +
		"TZ=Europe/Tallinn\n" +
		"PUID=1000\n" +
		"PGID=1000\n" +
		"DATA_ROOT=/srv/" + name + "\n" +
		"\n" +
		"# Stored as a keyed digest, like every value Silt does not recognise\n" +
		"# as safe. Click a line on \"What to hide\" to change its mind either way.\n" +
		"POSTGRES_PASSWORD=b8Xk2s-" + name + "-9fQ\n" +
		"SMTP_PASSWORD=correct-horse-battery-staple\n"
}
