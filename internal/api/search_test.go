package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

type searchResults struct {
	Query    string `json:"query"`
	Total    int    `json:"total"`
	Projects []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"projects"`
	Services []struct {
		Service     string `json:"service"`
		ProjectID   int64  `json:"project_id"`
		ProjectName string `json:"project_name"`
	} `json:"services"`
	EnvKeys []struct {
		Key      string `json:"key"`
		Service  string `json:"service"`
		Readable bool   `json:"readable"`
	} `json:"env_keys"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
	Events []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"events"`
}

func (f *fixture) search(t *testing.T, query string) searchResults {
	t.Helper()
	resp, body := f.get(t, "/api/search?q="+query)
	if resp.StatusCode != 200 {
		t.Fatalf("search %q = %d %s", query, resp.StatusCode, body)
	}
	var out searchResults
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func TestSearchFindsAProjectByName(t *testing.T) {
	f := newFixture(t)
	got := f.search(t, "media")
	if len(got.Projects) == 0 {
		t.Fatalf("no project matched: %+v", got)
	}
	if got.Projects[0].Name != "media" {
		t.Errorf("matched %q, want media", got.Projects[0].Name)
	}
	if got.Total == 0 {
		t.Error("total is zero despite a match")
	}
}

func TestSearchFindsAServiceAndNamesItsProject(t *testing.T) {
	f := newFixture(t)
	got := f.search(t, "radarr")
	if len(got.Services) == 0 {
		t.Fatalf("no service matched: %+v", got)
	}
	// The whole point: the result says which project to go to.
	if got.Services[0].ProjectName == "" || got.Services[0].ProjectID == 0 {
		t.Errorf("service result does not name its project: %+v", got.Services[0])
	}
}

// A service observed many times must be one row, or a stable stack drowns the
// results in copies of itself.
func TestSearchGroupsRepeatedObservations(t *testing.T) {
	f := newFixture(t)
	got := f.search(t, "radarr")
	seen := map[string]int{}
	for _, s := range got.Services {
		seen[s.ProjectName+"/"+s.Service]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("%s appears %d times; observations should be grouped", key, count)
		}
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	f := newFixture(t)
	lower := f.search(t, "radarr")
	upper := f.search(t, "RADARR")
	if len(upper.Services) != len(lower.Services) {
		t.Errorf("RADARR matched %d services, radarr matched %d", len(upper.Services), len(lower.Services))
	}
}

func TestSearchFindsEventsByTypeAndMessage(t *testing.T) {
	f := newFixture(t)
	byType := f.search(t, "container.die")
	if len(byType.Events) == 0 {
		t.Errorf("no event matched its type: %+v", byType)
	}
	byMessage := f.search(t, "die")
	if len(byMessage.Events) == 0 {
		t.Errorf("no event matched its message: %+v", byMessage)
	}
}

// The terms people type contain LIKE's wildcards — `_` is in almost every
// environment key — so they must be matched literally.
func TestSearchTreatsWildcardsAsLiteralCharacters(t *testing.T) {
	f := newFixture(t)
	// `_` would match any single character under LIKE, so this would find
	// "radarr" if the query were being interpreted as a pattern.
	if got := f.search(t, "rada_r"); got.Total != 0 {
		t.Errorf("an underscore behaved as a wildcard: %+v", got)
	}
	if got := f.search(t, "%"); got.Total != 0 {
		t.Errorf("a percent sign behaved as a wildcard: %+v", got)
	}
}

// One character matches most of the database and answers nothing.
func TestSearchIgnoresTermsThatAreTooShort(t *testing.T) {
	f := newFixture(t)
	for _, query := range []string{"", "r", "%20"} {
		if got := f.search(t, query); got.Total != 0 {
			t.Errorf("query %q returned %d results, want none", query, got.Total)
		}
	}
}

func TestSearchReturnsEmptyListsRatherThanNulls(t *testing.T) {
	f := newFixture(t)
	resp, body := f.get(t, "/api/search?q=nothingmatchesthis")
	if resp.StatusCode != 200 {
		t.Fatalf("search = %d", resp.StatusCode)
	}
	// A null would make every consumer branch before iterating.
	for _, field := range []string{`"projects":[]`, `"services":[]`, `"env_keys":[]`, `"files":[]`, `"events":[]`} {
		if !strings.Contains(string(body), field) {
			t.Errorf("response is missing %s: %s", field, body)
		}
	}
}

// Environment values are keyed digests. Search must not become a way to find
// one by its content.
func TestSearchNeverMatchesEnvironmentValues(t *testing.T) {
	f := newFixture(t)
	got := f.search(t, "radarr")
	for _, key := range got.EnvKeys {
		if key.Key == "" {
			t.Error("an environment result has no key")
		}
	}
	// The fixture stores a redacted value; searching for its digest or any
	// value text must find nothing.
	if hit := f.search(t, "supersecret"); hit.Total != 0 {
		t.Errorf("a value-shaped term matched something: %+v", hit)
	}
}
