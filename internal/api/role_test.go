package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/auth"
)

// The write guard at the HTTP layer. The rule is one line — every unsafe
// method under /api that is not part of authenticating needs an administrator
// — and these are the cases that make a per-route list grow holes instead.

// viewerServer is forward auth with an admin group nobody in these tests is in
// unless they say so.
func viewerServer(t *testing.T) string {
	t.Helper()
	ts := authServer(t, true, "X-Remote-User", "", func(g *api.Gate) {
		p, err := auth.NewProxy(true, "X-Remote-User", nil)
		if err != nil {
			t.Fatalf("NewProxy: %v", err)
		}
		g.Proxy = p.WithAdminGroups("X-Remote-Groups", []string{"silt-admins"})
	})
	return ts.URL
}

func TestAViewerReadsEverything(t *testing.T) {
	url := viewerServer(t)
	client := &http.Client{}
	viewer := map[string]string{"X-Remote-User": "reader", "X-Remote-Groups": "users"}

	// Reading the journal is the whole point of a viewer, and every screen
	// has to work or the role is useless rather than restricted.
	for _, path := range []string{
		"/api/projects", "/api/overview", "/api/events?limit=10",
		"/api/timeline", "/api/settings", "/api/hosts", "/api/audit?limit=10",
		"/api/search?q=x", "/api/version",
	} {
		if code, body := status(t, client, http.MethodGet, url+path, viewer, ""); code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200: %s", path, code, body)
		}
	}
}

func TestAViewerCannotChangeSiltsConfiguration(t *testing.T) {
	url := viewerServer(t)
	client := &http.Client{}
	viewer := map[string]string{
		"X-Remote-User":   "reader",
		"X-Remote-Groups": "users",
		"Content-Type":    "application/json",
		// Same-origin, so the refusal under test is the role and not CSRF.
		"Sec-Fetch-Site": "same-origin",
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/settings", `{"log_level":"debug"}`},
		{http.MethodDelete, "/api/settings", ""},
		{http.MethodPost, "/api/settings/notifications/test", ""},
		{http.MethodPost, "/api/maintenance/prune", ""},
		{http.MethodPost, "/api/projects/1/snapshot", ""},
		{http.MethodPost, "/api/projects/1/redaction-rules", `{"action":"hide","kind":"line","line_no":1}`},
		{http.MethodDelete, "/api/projects/1/redaction-rules/1", ""},
		{http.MethodPut, "/api/auth/password", `{"password":"hunter2hunter2"}`},
		{http.MethodPut, "/api/auth/account", `{"enabled":false}`},
		{http.MethodDelete, "/api/auth/sessions", ""},
		{http.MethodDelete, "/api/auth/link", ""},
	} {
		code, body := status(t, client, tc.method, url+tc.path, viewer, tc.body)
		if code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403: %s", tc.method, tc.path, code, body)
		}
	}
}

func TestAnAdministratorIsNotRefusedByTheRoleCheck(t *testing.T) {
	url := viewerServer(t)
	client := &http.Client{}
	admin := map[string]string{
		"X-Remote-User":   "operator",
		"X-Remote-Groups": "users,silt-admins",
		"Content-Type":    "application/json",
		"Sec-Fetch-Site":  "same-origin",
	}

	// Not asserting success — these need state a bare test server does not
	// have — only that the refusal is never the role check.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/settings", `{"log_level":"debug"}`},
		{http.MethodPost, "/api/maintenance/prune", ""},
	} {
		if code, body := status(t, client, tc.method, url+tc.path, admin, tc.body); code == http.StatusForbidden {
			t.Errorf("%s %s = 403 for an administrator: %s", tc.method, tc.path, body)
		}
	}
}

func TestLoggingOutIsNotAnAdministrativeAction(t *testing.T) {
	// A viewer who cannot end their own session would be a viewer stuck
	// signed in. Logout is public, so it never reaches the role check — this
	// is the test that notices if it stops being.
	url := viewerServer(t)
	client := &http.Client{}
	viewer := map[string]string{
		"X-Remote-User":   "reader",
		"X-Remote-Groups": "users",
		"Sec-Fetch-Site":  "same-origin",
	}
	if code, body := status(t, client, http.MethodPost, url+"/api/logout", viewer, ""); code == http.StatusForbidden {
		t.Errorf("POST /api/logout = 403 for a viewer: %s", body)
	}
}

func TestTheIngestWebhookIsNotAffectedByRoles(t *testing.T) {
	// It carries its own token and is reached by machines that present no
	// identity at all. Putting it behind the role check would break every
	// external event source on upgrade.
	url := viewerServer(t)
	client := &http.Client{}
	headers := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}
	code, body := status(t, client, http.MethodPost,
		url+"/api/ingest?token=ingest-token", headers,
		`{"type":"monitor.down","message":"probe failed","severity":"error"}`)
	if code == http.StatusForbidden {
		t.Errorf("POST /api/ingest = 403: %s", body)
	}
}

func TestTheAuthStateReportsTheRole(t *testing.T) {
	// The UI reads this to stop offering controls that would be refused: a
	// save button that always fails is worse than no save button.
	url := viewerServer(t)
	client := &http.Client{}

	for _, tc := range []struct{ groups, want string }{
		{"users", "viewer"},
		{"users,silt-admins", "admin"},
	} {
		_, body := status(t, client, http.MethodGet, url+"/api/auth",
			map[string]string{"X-Remote-User": "someone", "X-Remote-Groups": tc.groups}, "")
		if !strings.Contains(body, `"role":"`+tc.want+`"`) {
			t.Errorf("groups %q: auth state does not report role %q: %s", tc.groups, tc.want, body)
		}
	}
}
