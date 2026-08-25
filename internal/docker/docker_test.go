package docker

import (
	"reflect"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func summary(id, project, service, workdir, configFiles string) container.Summary {
	return container.Summary{
		ID:    id,
		Names: []string{"/" + project + "-" + service + "-1"},
		Image: "example/" + service,
		State: "running",
		Labels: map[string]string{
			LabelProject:     project,
			LabelService:     service,
			LabelWorkingDir:  workdir,
			LabelConfigFiles: configFiles,
		},
	}
}

func TestGroupByProject(t *testing.T) {
	// Docker returns containers newest-first and interleaved across projects.
	list := []container.Summary{
		summary("c3", "media", "sonarr", "/srv/media", "/srv/media/compose.yaml"),
		summary("c1", "tools", "vaultwarden", "/srv/tools", "/srv/tools/compose.yaml,/srv/tools/override.yaml"),
		summary("c2", "media", "radarr", "/srv/media", "/srv/media/compose.yaml"),
	}

	got := groupByProject(list)

	if len(got) != 2 {
		t.Fatalf("got %d projects, want 2", len(got))
	}
	// Projects sort by name so identical observations hash identically later.
	if got[0].Name != "media" || got[1].Name != "tools" {
		t.Fatalf("project order = %q, %q; want media, tools", got[0].Name, got[1].Name)
	}
	if got[0].WorkingDir != "/srv/media" {
		t.Errorf("working dir = %q, want /srv/media", got[0].WorkingDir)
	}
	// Services sort by name for the same reason.
	if len(got[0].Services) != 2 || got[0].Services[0].Name != "radarr" || got[0].Services[1].Name != "sonarr" {
		t.Errorf("media services = %+v, want radarr then sonarr", got[0].Services)
	}
	if got[0].Services[0].ContainerName != "media-radarr-1" {
		t.Errorf("container name = %q, want media-radarr-1 (leading slash stripped)", got[0].Services[0].ContainerName)
	}
	want := []string{"/srv/tools/compose.yaml", "/srv/tools/override.yaml"}
	if !reflect.DeepEqual(got[1].ConfigFiles, want) {
		t.Errorf("config files = %v, want %v", got[1].ConfigFiles, want)
	}
}

func TestGroupByProjectSkipsUnlabelled(t *testing.T) {
	list := []container.Summary{
		{ID: "x", Labels: map[string]string{}},
		summary("c1", "media", "radarr", "/srv/media", ""),
	}
	got := groupByProject(list)
	if len(got) != 1 || got[0].Name != "media" {
		t.Fatalf("got %+v, want only the media project", got)
	}
}

// Project-level labels are written to every container, so a container missing
// one must not blank out a value another container supplied.
func TestGroupByProjectFillsGaps(t *testing.T) {
	a := summary("c1", "media", "radarr", "", "")
	b := summary("c2", "media", "sonarr", "/srv/media", "/srv/media/compose.yaml")
	got := groupByProject([]container.Summary{a, b})
	if got[0].WorkingDir != "/srv/media" {
		t.Errorf("working dir = %q, want /srv/media", got[0].WorkingDir)
	}
	if len(got[0].ConfigFiles) != 1 {
		t.Errorf("config files = %v, want one entry", got[0].ConfigFiles)
	}
}

func TestSplitConfigFiles(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"/a/compose.yaml", []string{"/a/compose.yaml"}},
		{"/a/compose.yaml,/a/override.yaml", []string{"/a/compose.yaml", "/a/override.yaml"}},
		{" /a/one.yaml , /a/two.yaml ", []string{"/a/one.yaml", "/a/two.yaml"}},
		{"/a/one.yaml,,", []string{"/a/one.yaml"}},
	}
	for _, tt := range tests {
		if got := splitConfigFiles(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitConfigFiles(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIsNoise(t *testing.T) {
	tests := []struct {
		action string
		want   bool
	}{
		// Docker appends the command to exec actions, so equality matching
		// would let every healthcheck probe through.
		{"exec_create: /healthcheck.sh", true},
		{"exec_start: /bin/sh -c 'curl -f localhost'", true},
		{"exec_create", true},
		{"exec_die", true},
		{"top", true},
		{"resize", true},
		{"archive-path", true},
		// Real signals must survive.
		{"start", false},
		{"die", false},
		{"stop", false},
		{"restart", false},
		{"health_status: healthy", false},
		{"health_status: unhealthy", false},
		{"pull", false},
		{"create", false},
		// Not a prefix match on an unrelated action that merely starts the same.
		{"topology-change", false},
		{"exporter", false},
	}
	for _, tt := range tests {
		if got := isNoise(tt.action); got != tt.want {
			t.Errorf("isNoise(%q) = %v, want %v", tt.action, got, tt.want)
		}
	}
}

func TestBackoffDelayStaysInBounds(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second}
	for attempt := 0; attempt < 64; attempt++ {
		d := b.delay(attempt)
		if d < b.Min {
			t.Fatalf("attempt %d: delay = %v, want >= %v (no unfloored jitter)", attempt, d, b.Min)
		}
		if d > b.Max {
			t.Fatalf("attempt %d: delay = %v, want <= %v", attempt, d, b.Max)
		}
	}
}

func TestBackoffDelayGrows(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second}
	// Full jitter makes any single draw unpredictable, so compare ceilings by
	// taking the largest of many draws.
	maxOf := func(attempt int) time.Duration {
		var m time.Duration
		for i := 0; i < 200; i++ {
			if d := b.delay(attempt); d > m {
				m = d
			}
		}
		return m
	}
	if maxOf(0) >= maxOf(3) {
		t.Errorf("ceiling did not grow between attempt 0 (%v) and 3 (%v)", maxOf(0), maxOf(3))
	}
}
