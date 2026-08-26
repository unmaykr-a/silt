package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/store"
)

func authServer(t *testing.T, trustProxy bool, header, passwordHash string) *httptest.Server {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auth, err := api.NewAuth(trustProxy, header, passwordHash)
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	srv := api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, nil,
		config.Config{IngestToken: "ingest-token"}, nil)
	srv.SetAuth(auth)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func status(t *testing.T, client *http.Client, method, url string, headers map[string]string, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// With nothing configured Silt stays open, which is the documented default for
// a tool people put behind their own proxy.
func TestNoAuthConfiguredLeavesEverythingOpen(t *testing.T) {
	ts := authServer(t, false, "", "")
	if code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts", nil, ""); code != 200 {
		t.Errorf("GET /api/hosts = %d, want 200", code)
	}
}

func TestForwardAuthRequiresTheHeader(t *testing.T) {
	ts := authServer(t, true, "X-Remote-User", "")

	if code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("without header = %d, want 401", code)
	}
	if code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts",
		map[string]string{"X-Remote-User": "andri"}, ""); code != 200 {
		t.Errorf("with header = %d, want 200", code)
	}
	// A header present but empty is not an identity.
	if code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts",
		map[string]string{"X-Remote-User": "   "}, ""); code != http.StatusUnauthorized {
		t.Errorf("blank header = %d, want 401", code)
	}
}

func TestForwardAuthHonoursCustomHeaderName(t *testing.T) {
	ts := authServer(t, true, "X-Authentik-Username", "")

	if code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts",
		map[string]string{"X-Remote-User": "andri"}, ""); code != http.StatusUnauthorized {
		t.Error("the default header was accepted despite a custom name being configured")
	}
	if code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts",
		map[string]string{"X-Authentik-Username": "andri"}, ""); code != 200 {
		t.Error("the configured header was not accepted")
	}
}

func TestPasswordLoginFlow(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ts := authServer(t, false, "", string(hash))

	jar := newJar(t)
	client := &http.Client{Jar: jar}

	if code, _ := status(t, client, "GET", ts.URL+"/api/hosts", nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("before login = %d, want 401", code)
	}
	if code, _ := status(t, client, "POST", ts.URL+"/api/login", nil, `{"password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", code)
	}
	if code, _ := status(t, client, "POST", ts.URL+"/api/login", nil, `{"password":"correct horse"}`); code != 200 {
		t.Fatalf("correct password = %d, want 200", code)
	}
	if code, _ := status(t, client, "GET", ts.URL+"/api/hosts", nil, ""); code != 200 {
		t.Fatalf("after login = %d, want 200", code)
	}
	if code, _ := status(t, client, "POST", ts.URL+"/api/logout", nil, ""); code != 200 {
		t.Fatalf("logout = %d, want 200", code)
	}
	if code, _ := status(t, client, "GET", ts.URL+"/api/hosts", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("after logout = %d, want 401", code)
	}
}

// A forged or tampered session cookie must not be accepted.
func TestForgedSessionCookieIsRejected(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	ts := authServer(t, false, "", string(hash))

	for _, value := range []string{
		"9999999999.deadbeef",
		"9999999999",
		"",
		"notanumber.abc",
	} {
		code, _ := status(t, ts.Client(), "GET", ts.URL+"/api/hosts",
			map[string]string{"Cookie": "silt_session=" + value}, "")
		if code != http.StatusUnauthorized {
			t.Errorf("forged cookie %q = %d, want 401", value, code)
		}
	}
}

// Probes and the ingest webhook must stay reachable: an orchestrator cannot
// present a session, and ingest carries its own token.
func TestPublicPathsBypassAuth(t *testing.T) {
	ts := authServer(t, true, "X-Remote-User", "")

	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/auth"} {
		if code, _ := status(t, ts.Client(), "GET", ts.URL+path, nil, ""); code != 200 {
			t.Errorf("%s = %d, want 200 without auth", path, code)
		}
	}
	// Ingest is reachable but still enforces its own token.
	if code, _ := status(t, ts.Client(), "POST", ts.URL+"/api/ingest", nil, `{"type":"x"}`); code != http.StatusUnauthorized {
		t.Errorf("ingest without its token = %d, want 401 from the token check", code)
	}
	if code, _ := status(t, ts.Client(), "POST", ts.URL+"/api/ingest?token=ingest-token", nil,
		`{"type":"x"}`); code != http.StatusAccepted {
		t.Errorf("ingest with its token = %d, want 202", code)
	}
}

// The SPA has to load in order to render a login form.
func TestUnauthenticatedBrowserStillGetsTheApp(t *testing.T) {
	ts := authServer(t, true, "X-Remote-User", "")
	code, _ := status(t, ts.Client(), "GET", ts.URL+"/", nil, "")
	if code == http.StatusUnauthorized {
		t.Error("an unauthenticated navigation was refused; the login form could never render")
	}
}

func TestAuthStateReporting(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	ts := authServer(t, false, "", string(hash))

	_, body := status(t, ts.Client(), "GET", ts.URL+"/api/auth", nil, "")
	var state struct {
		Required        bool `json:"required"`
		PasswordEnabled bool `json:"password_enabled"`
		Authenticated   bool `json:"authenticated"`
	}
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !state.Required || !state.PasswordEnabled || state.Authenticated {
		t.Errorf("auth state = %+v, want required + password, not yet authenticated", state)
	}
	if strings.Contains(body, "$2a$") || strings.Contains(body, "$2b$") {
		t.Errorf("auth state leaked the password hash: %s", body)
	}
}

// A malformed hash would otherwise lock the owner out silently.
func TestInvalidPasswordHashIsRejectedAtStartup(t *testing.T) {
	if _, err := api.NewAuth(false, "", "not-a-bcrypt-hash"); err == nil {
		t.Error("NewAuth accepted an invalid bcrypt hash")
	}
}

func newJar(t *testing.T) http.CookieJar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return jar
}
