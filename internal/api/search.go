package api

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/unmaykr-a/silt/internal/store"
)

// Search across everything Silt records.
//
// A host running forty stacks has no other way to answer "when did anything
// about radarr change?" — the alternative is opening projects one at a time
// until you find it. The categories are the four things people actually look
// for by name: a project, a service, an environment key, and something that
// happened.
//
// Every category is capped independently rather than sharing one budget, so a
// term matching a thousand events still shows the one project it matched.

type searchProject struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	WorkingDir string `json:"working_dir,omitempty"`
	LastSeenAt int64  `json:"last_seen_at"`
	Archived   bool   `json:"archived"`
}

type searchService struct {
	Service     string `json:"service"`
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name"`
	LastSeenAt  int64  `json:"last_seen_at"`
}

type searchEnvKey struct {
	Key         string `json:"key"`
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name"`
	Service     string `json:"service"`
	LastSeenAt  int64  `json:"last_seen_at"`
	// Readable is true when at least one observation of this key was kept in
	// cleartext, which is what the keep-list decides.
	Readable bool `json:"readable"`
}

type searchFile struct {
	Path        string `json:"path"`
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name"`
	LastSeenAt  int64  `json:"last_seen_at"`
}

type searchResponse struct {
	Query    string          `json:"query"`
	Projects []searchProject `json:"projects"`
	Services []searchService `json:"services"`
	EnvKeys  []searchEnvKey  `json:"env_keys"`
	Files    []searchFile    `json:"files"`
	Events   []eventResponse `json:"events"`
	// Total is what was found across every category, so the UI can say
	// "nothing" without inspecting five empty lists.
	Total int `json:"total"`
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	out := searchResponse{
		Query:    query,
		Projects: []searchProject{},
		Services: []searchService{},
		EnvKeys:  []searchEnvKey{},
		Files:    []searchFile{},
		Events:   []eventResponse{},
	}

	results, err := s.store.Search(r.Context(), query, store.SearchLimit)
	if err != nil {
		s.log.Error("search failed", "error", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	for _, hit := range results.Projects {
		out.Projects = append(out.Projects, searchProject{
			ID:         hit.ID,
			Name:       hit.Name,
			WorkingDir: hit.WorkingDir,
			LastSeenAt: hit.LastSeenAt,
			Archived:   hit.Archived,
		})
	}
	for _, hit := range results.Services {
		out.Services = append(out.Services, searchService{
			Service:     hit.Service,
			ProjectID:   hit.ProjectID,
			ProjectName: hit.ProjectName,
			LastSeenAt:  hit.LastSeenAt,
		})
	}
	for _, hit := range results.EnvKeys {
		out.EnvKeys = append(out.EnvKeys, searchEnvKey{
			Key:         hit.Key,
			ProjectID:   hit.ProjectID,
			ProjectName: hit.ProjectName,
			Service:     hit.Service,
			LastSeenAt:  hit.LastSeenAt,
			Readable:    hit.Readable,
		})
	}
	for _, hit := range results.Files {
		out.Files = append(out.Files, searchFile{
			Path:        hit.Path,
			ProjectID:   hit.ProjectID,
			ProjectName: hit.ProjectName,
			LastSeenAt:  hit.LastSeenAt,
		})
	}
	for _, hit := range results.Events {
		out.Events = append(out.Events, eventResponse{
			ID:        hit.ID,
			ProjectID: projectIDOf(hit.ProjectID),
			Service:   hit.Service,
			TS:        hit.TS,
			Source:    hit.Source,
			Type:      hit.Type,
			Severity:  hit.Severity,
			Message:   hit.Message,
		})
	}

	out.Total = len(out.Projects) + len(out.Services) + len(out.EnvKeys) +
		len(out.Files) + len(out.Events)
	writeJSON(w, http.StatusOK, out)
}

// projectIDOf turns a nullable column into the optional field the API exposes.
// A host-level event has no project.
func projectIDOf(id sql.NullInt64) *int64 {
	if !id.Valid {
		return nil
	}
	value := id.Int64
	return &value
}
