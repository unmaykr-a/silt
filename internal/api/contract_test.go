package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The spec at api/openapi.yaml is hand-maintained, which means nothing stops
// it drifting from the handlers except these tests. They check both
// directions: every documented operation is reachable and shaped as declared,
// and every registered route is documented.

type openAPI struct {
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
		Responses   map[string]struct {
			Content map[string]struct {
				Schema map[string]any `yaml:"schema"`
			} `yaml:"content"`
		} `yaml:"responses"`
	} `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Required   []string       `yaml:"required"`
			Properties map[string]any `yaml:"properties"`
			AllOf      []struct {
				Ref        string         `yaml:"$ref"`
				Required   []string       `yaml:"required"`
				Properties map[string]any `yaml:"properties"`
			} `yaml:"allOf"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) openAPI {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec openAPI
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("spec declares no paths")
	}
	return spec
}

// requiredFields resolves a schema name to its required property names,
// following a single level of allOf composition.
func (s openAPI) requiredFields(t *testing.T, name string) []string {
	t.Helper()
	schema, ok := s.Components.Schemas[name]
	if !ok {
		t.Fatalf("spec has no schema %q", name)
	}
	out := append([]string(nil), schema.Required...)
	for _, part := range schema.AllOf {
		if part.Ref != "" {
			out = append(out, s.requiredFields(t, strings.TrimPrefix(part.Ref, "#/components/schemas/"))...)
			continue
		}
		out = append(out, part.Required...)
	}
	return out
}

// contractCase is one documented operation exercised against a live server.
type contractCase struct {
	method     string
	url        string
	body       string
	headers    map[string]string
	wantStatus int
	// schema names the response schema whose required fields must be present.
	schema string
	// element is true when the response is an array of that schema.
	element bool
}

func TestSpecOperationsMatchHandlers(t *testing.T) {
	spec := loadSpec(t)
	f := newFixture(t)
	auth := map[string]string{"Authorization": "Bearer " + f.ingestTok}

	cases := map[string]contractCase{
		"listHosts":    {method: "GET", url: "/api/hosts", wantStatus: 200, schema: "Host", element: true},
		"listProjects": {method: "GET", url: "/api/projects", wantStatus: 200, schema: "Project", element: true},
		"getProject":   {method: "GET", url: "/api/projects/1", wantStatus: 200, schema: "Project"},
		"listSnapshots": {
			method: "GET", url: "/api/projects/1/snapshots?limit=10&changed_only=false",
			wantStatus: 200, schema: "Snapshot", element: true,
		},
		"takeSnapshot": {method: "POST", url: "/api/projects/1/snapshot", wantStatus: 202},
		"getSnapshot":  {method: "GET", url: "/api/snapshots/1", wantStatus: 200, schema: "SnapshotDetail"},
		"getCompose":   {method: "GET", url: "/api/snapshots/1/compose", wantStatus: 200, schema: "ProjectModel"},
		"getDiff":      {method: "GET", url: "/api/diff?from=1&to=2", wantStatus: 200, schema: "Diff"},
		"listEvents":   {method: "GET", url: "/api/events", wantStatus: 200, schema: "Event", element: true},
		"listProjectServices": {
			method: "GET", url: "/api/projects/1/services", wantStatus: 200,
		},
		"getServiceHistory": {
			method: "GET", url: "/api/projects/1/services/radarr", wantStatus: 200,
			schema: "ServiceHistory",
		},
		"getSettings": {method: "GET", url: "/api/settings", wantStatus: 200, schema: "Settings"},
		"updateSettings": {
			method: "PUT", url: "/api/settings", body: `{"retention_days":30}`,
			wantStatus: 200, schema: "Settings",
		},
		"resetSettings": {method: "DELETE", url: "/api/settings", wantStatus: 200, schema: "Settings"},
		"getVersion":    {method: "GET", url: "/api/version", wantStatus: 200, schema: "VersionInfo"},
		"getAuthState":  {method: "GET", url: "/api/auth", wantStatus: 200, schema: "AuthState"},
		// The fixture configures no provider, so these report absence. Their
		// success paths are covered by dedicated tests against a fake provider.
		"startOIDCLogin":  {method: "GET", url: "/api/auth/login", wantStatus: 503},
		"finishOIDCLogin": {method: "GET", url: "/api/auth/callback", wantStatus: 503},
		"getSessions":     {method: "GET", url: "/api/auth/sessions", wantStatus: 200, schema: "SessionCount"},
		// The built-in account is off in this fixture, so these report absence.
		// Their success paths are covered in account_test.go against a fixture
		// that has one.
		"setupAccount":      {method: "POST", url: "/api/auth/setup", body: `{"password":"a-long-enough-one"}`, wantStatus: 409},
		"changePassword":    {method: "PUT", url: "/api/auth/password", body: `{"current":"x","password":"y"}`, wantStatus: 403},
		"setAccountEnabled": {method: "PUT", url: "/api/auth/account", body: `{"enabled":false}`, wantStatus: 503},
		"startAccountLink":  {method: "GET", url: "/api/auth/link", wantStatus: 503},
		"removeAccountLink": {method: "DELETE", url: "/api/auth/link", wantStatus: 503},
		"revokeSessions":    {method: "DELETE", url: "/api/auth/sessions", wantStatus: 200},
		// The fixture configures no password, so login reports that it is not
		// available rather than accepting anything.
		"login":                {method: "POST", url: "/api/login", body: `{"password":"x"}`, wantStatus: 503},
		"logout":               {method: "POST", url: "/api/logout", wantStatus: 200},
		"listSnapshotFiles":    {method: "GET", url: "/api/snapshots/1/files", wantStatus: 200},
		"listProjectFilePaths": {method: "GET", url: "/api/projects/1/files", wantStatus: 200},
		"listRedactionRules":   {method: "GET", url: "/api/projects/1/redaction-rules", wantStatus: 200},
		"createRedactionRule": {
			method: "POST", url: "/api/projects/1/redaction-rules",
			body:       `{"action":"hide","kind":"key","key":"SMTP_PASSWORD"}`,
			wantStatus: 201, schema: "RedactionRule",
		},
		// The fixture captures no files, so these report absence rather than
		// content. Their success paths are covered by dedicated tests.
		"getSnapshotFile":     {method: "GET", url: "/api/snapshots/1/file?path=/nope.yaml", wantStatus: 404},
		"getFileDiff":         {method: "GET", url: "/api/diff/file?from=1&to=2&path=/nope.yaml", wantStatus: 200, schema: "FileDiff"},
		"previewFile":         {method: "GET", url: "/api/projects/1/files/preview?path=/nope.yaml", wantStatus: 503},
		"deleteRedactionRule": {method: "DELETE", url: "/api/projects/1/redaction-rules/999", wantStatus: 404},
		"prune":               {method: "POST", url: "/api/maintenance/prune", wantStatus: 200, schema: "PruneResult"},
		"getTimeline":         {method: "GET", url: "/api/timeline", wantStatus: 200, schema: "Timeline"},
		"search":              {method: "GET", url: "/api/search?q=radarr", wantStatus: 200, schema: "SearchResults"},
		"overview":            {method: "GET", url: "/api/overview", wantStatus: 200, schema: "Overview"},
		"listAudit":           {method: "GET", url: "/api/audit", wantStatus: 200, schema: "AuditLog"},
		"testNotifications":   {method: "POST", url: "/api/settings/notifications/test", wantStatus: 200, schema: "NotifyTestResults"},
		"ingest": {
			method: "POST", url: "/api/ingest", body: `{"type":"contract.test"}`,
			headers: auth, wantStatus: 202,
		},
		"healthz": {method: "GET", url: "/healthz", wantStatus: 200},
		"readyz":  {method: "GET", url: "/readyz", wantStatus: 200},
		"metrics": {method: "GET", url: "/metrics", wantStatus: 200},
		// SSE holds the connection open, so it is covered by its own test
		// rather than a request/response assertion here.
		"stream": {},
	}

	for path, methods := range spec.Paths {
		for method, op := range methods {
			if op.OperationID == "" {
				t.Errorf("%s %s has no operationId", strings.ToUpper(method), path)
				continue
			}
			tc, ok := cases[op.OperationID]
			if !ok {
				t.Errorf("operation %q is documented but not exercised by the contract test", op.OperationID)
				continue
			}
			if tc.url == "" {
				continue // deliberately covered elsewhere
			}

			t.Run(op.OperationID, func(t *testing.T) {
				var resp *http.Response
				var body []byte
				switch tc.method {
				case http.MethodPost:
					resp, body = f.post(t, tc.url, tc.body, tc.headers)
				case http.MethodPut, http.MethodDelete:
					resp, body = f.do(t, tc.method, tc.url, tc.body, tc.headers)
				default:
					resp, body = f.get(t, tc.url)
				}

				if resp.StatusCode != tc.wantStatus {
					t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.wantStatus, body)
				}
				if _, declared := op.Responses[itoa(tc.wantStatus)]; !declared {
					t.Errorf("handler returned %d but the spec does not declare it", tc.wantStatus)
				}
				if tc.schema == "" {
					return
				}

				required := spec.requiredFields(t, tc.schema)
				for _, obj := range decodeObjects(t, body, tc.element) {
					for _, field := range required {
						if _, ok := obj[field]; !ok {
							t.Errorf("response is missing required field %q declared on %s: %s",
								field, tc.schema, body)
						}
					}
				}
			})
		}
	}
}

// decodeObjects returns the response as one or more JSON objects.
//
// A required field that is absent from an empty array cannot be checked, so an
// empty collection is reported rather than silently passing.
func decodeObjects(t *testing.T, body []byte, isArray bool) []map[string]any {
	t.Helper()
	if !isArray {
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("decode object: %v (%s)", err, body)
		}
		return []map[string]any{obj}
	}
	var arr []map[string]any
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode array: %v (%s)", err, body)
	}
	if len(arr) == 0 {
		t.Fatal("collection was empty, so required fields were not actually checked")
	}
	return arr
}

// routePattern matches the mux registrations in server.go.
var routePattern = regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PUT|DELETE|PATCH) ([^"]+)"`)

// Every route the server registers must be documented. Without this, adding a
// handler and forgetting the spec would go unnoticed until the frontend types
// came back missing.
func TestEveryRouteIsDocumented(t *testing.T) {
	spec := loadSpec(t)

	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	matches := routePattern.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("found no route registrations; the pattern needs updating")
	}

	var undocumented []string
	for _, m := range matches {
		method, path := strings.ToLower(m[1]), m[2]
		methods, ok := spec.Paths[path]
		if !ok {
			undocumented = append(undocumented, m[1]+" "+path)
			continue
		}
		if _, ok := methods[method]; !ok {
			undocumented = append(undocumented, m[1]+" "+path)
		}
	}
	sort.Strings(undocumented)
	if len(undocumented) > 0 {
		t.Errorf("routes registered but absent from api/openapi.yaml:\n  %s",
			strings.Join(undocumented, "\n  "))
	}
}

func itoa(n int) string {
	return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}
