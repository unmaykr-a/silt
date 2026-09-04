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
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/store"
)

// accountFixture is a Silt with the built-in account switched on, which is the
// default shape of a fresh install.
type accountFixture struct {
	srv     *httptest.Server
	client  *http.Client
	account *auth.Account
	gate    *api.Gate
	db      *store.Store
}

func newAccountFixture(t *testing.T, envHash string, opts ...func(*api.Gate)) *accountFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	account, err := auth.LoadAccount(ctx, db, envHash, true)
	if err != nil {
		t.Fatalf("LoadAccount: %v", err)
	}
	proxy, err := auth.NewProxy(false, "", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	gate := &api.Gate{
		Sessions: auth.NewSessions(db, time.Hour, 0),
		Account:  account,
		Proxy:    proxy,
	}
	for _, opt := range opts {
		opt(gate)
	}

	srv := api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, nil, config.Config{}, nil)
	srv.SetAuth(gate)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &accountFixture{srv: ts, client: &http.Client{Jar: jar}, account: account, gate: gate, db: db}
}

func (f *accountFixture) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	return status(t, f.client, method, f.srv.URL+path, nil, body)
}

func (f *accountFixture) state(t *testing.T) map[string]any {
	t.Helper()
	_, body := f.do(t, "GET", "/api/auth", "")
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode auth state: %v (%s)", err, body)
	}
	return out
}

const goodPassword = "correct horse battery"

// The point of the whole change: a fresh install is closed, not open, and the
// only thing you can do is choose a password.
func TestFreshInstallIsClosedUntilSetUp(t *testing.T) {
	f := newAccountFixture(t, "")

	if code, _ := f.do(t, "GET", "/api/hosts", ""); code != http.StatusUnauthorized {
		t.Errorf("a fresh install answered /api/hosts with %d; it must be closed", code)
	}
	state := f.state(t)
	if state["setup_required"] != true || state["required"] != true {
		t.Errorf("auth state = %v, want setup_required and required", state)
	}
	// The SPA still has to load, or the setup form could never render.
	if code, _ := f.do(t, "GET", "/", ""); code == http.StatusUnauthorized {
		t.Error("the app itself was refused; the setup form could never render")
	}
}

func TestSetupClaimsTheAccountAndSignsIn(t *testing.T) {
	f := newAccountFixture(t, "")

	code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`)
	if code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	// Signed in straight away: making someone retype what they just chose is
	// ceremony.
	if code, _ := f.do(t, "GET", "/api/hosts", ""); code != 200 {
		t.Errorf("after setup = %d, want 200", code)
	}
	state := f.state(t)
	if state["setup_required"] != false || state["password_enabled"] != true {
		t.Errorf("auth state = %v, want a claimed account", state)
	}
}

// It has to work exactly once, or the setup endpoint would be a way to take
// the install over at any time.
func TestSetupWorksOnlyOnce(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}

	// A different client, with no session, must not be able to reset it.
	other := newJar(t)
	code, _ := status(t, &http.Client{Jar: other}, "POST", f.srv.URL+"/api/auth/setup", nil,
		`{"password":"a-completely-different-one"}`)
	if code != http.StatusConflict {
		t.Errorf("second setup = %d, want 409", code)
	}
	// And the original password still works.
	if code, _ := f.do(t, "POST", "/api/login", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Error("the first password stopped working after a refused second setup")
	}
}

func TestSetupRefusesAWeakPassword(t *testing.T) {
	f := newAccountFixture(t, "")
	for _, weak := range []string{"", "short", "         "} {
		code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+weak+`"}`)
		if code != http.StatusBadRequest {
			t.Errorf("password %q = %d %s, want 400", weak, code, body)
		}
	}
	if !f.account.SetupRequired() {
		t.Error("a refused password left the account claimed")
	}
}

// An environment hash claims the account before Silt starts, which is how
// someone managing it declaratively avoids the first-run window entirely.
func TestEnvironmentHashClaimsTheAccountAndOwnsThePassword(t *testing.T) {
	hash := bcryptOf(t, goodPassword)
	f := newAccountFixture(t, hash)

	state := f.state(t)
	if state["setup_required"] != false {
		t.Error("an environment hash left the account waiting for setup")
	}
	if state["local_managed"] != true {
		t.Error("the UI was not told the password comes from the environment")
	}
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != http.StatusConflict {
		t.Error("setup was allowed over an environment-managed password")
	}
	if code, body := f.do(t, "POST", "/api/login", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("login with the environment password = %d %s", code, body)
	}
	// The UI must not be able to change what the environment owns, or the two
	// would silently diverge.
	code, _ := f.do(t, "PUT", "/api/auth/password",
		`{"current":"`+goodPassword+`","password":"something-else-entirely"}`)
	if code != http.StatusBadRequest {
		t.Errorf("changing an environment-managed password = %d, want 400", code)
	}
}

func TestChangePasswordRequiresTheCurrentOne(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("setup failed")
	}

	// A session someone walked away from is not enough.
	if code, _ := f.do(t, "PUT", "/api/auth/password",
		`{"current":"not-it","password":"a-brand-new-password"}`); code != http.StatusBadRequest {
		t.Error("the password changed without the current one")
	}
	if code, body := f.do(t, "PUT", "/api/auth/password",
		`{"current":"`+goodPassword+`","password":"a-brand-new-password"}`); code != 200 {
		t.Fatalf("change = %d %s", code, body)
	}
	// This session keeps working, because it was reissued.
	if code, _ := f.do(t, "GET", "/api/hosts", ""); code != 200 {
		t.Error("the session that changed the password was dropped")
	}
	if code, _ := f.do(t, "POST", "/api/login", `{"password":"a-brand-new-password"}`); code != 200 {
		t.Error("the new password does not work")
	}
}

// Changing the password because you think it leaked should end whatever leaked.
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("setup failed")
	}

	// A second browser, signed in with the same password.
	other := &http.Client{Jar: newJar(t)}
	if code, _ := status(t, other, "POST", f.srv.URL+"/api/login", nil,
		`{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("the second client could not sign in")
	}
	if code, _ := status(t, other, "GET", f.srv.URL+"/api/hosts", nil, ""); code != 200 {
		t.Fatal("the second client is not signed in")
	}

	if code, _ := f.do(t, "PUT", "/api/auth/password",
		`{"current":"`+goodPassword+`","password":"a-brand-new-password"}`); code != 200 {
		t.Fatal("change failed")
	}
	if code, _ := status(t, other, "GET", f.srv.URL+"/api/hosts", nil, ""); code != http.StatusUnauthorized {
		t.Error("the other session survived a password change")
	}
}

// Disabling the only way in would lock the owner out of their own install.
func TestDisablingTheAccountIsRefusedWhenNothingElseWouldLetYouIn(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("setup failed")
	}

	code, body := f.do(t, "PUT", "/api/auth/account", `{"enabled":false}`)
	if code != http.StatusConflict {
		t.Errorf("disable = %d %s, want 409", code, body)
	}
	if !f.account.Enabled() {
		t.Error("the account was disabled anyway")
	}
}

func TestDisablingTheAccountIsAllowedWhenAProxyCanLetYouIn(t *testing.T) {
	f := newAccountFixture(t, "", func(g *api.Gate) {
		proxy, err := auth.NewProxy(true, "X-Remote-User", nil)
		if err != nil {
			t.Fatalf("NewProxy: %v", err)
		}
		g.Proxy = proxy
	})
	// A proxy is configured, so claiming the account needs a session — see
	// TestSetupRequiresASessionWhenAProviderCanLetYouIn.
	proxied := map[string]string{"X-Remote-User": "andri"}
	if code, body := status(t, f.client, "POST", f.srv.URL+"/api/auth/setup", proxied,
		`{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}

	if code, body := f.do(t, "PUT", "/api/auth/account", `{"enabled":false}`); code != 200 {
		t.Fatalf("disable = %d %s", code, body)
	}
	if f.account.Enabled() {
		t.Error("the account is still enabled")
	}
	// The session that disabled it is gone, and the password no longer works.
	if code, _ := f.do(t, "POST", "/api/login", `{"password":"`+goodPassword+`"}`); code == 200 {
		t.Error("password sign-in still works after disabling the account")
	}
	// The proxy still gets in, which is the whole reason this was allowed.
	code, _ := status(t, f.client, "GET", f.srv.URL+"/api/hosts",
		map[string]string{"X-Remote-User": "andri"}, "")
	if code != 200 {
		t.Errorf("the proxy identity was refused: %d", code)
	}
}

// Nobody signed in should be able to reach the account endpoints.
func TestAccountEndpointsRequireTheAccountItself(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("setup failed")
	}

	stranger := &http.Client{Jar: newJar(t)}
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/auth/password", `{"current":"x","password":"y"}`},
		{"PUT", "/api/auth/account", `{"enabled":false}`},
		{"DELETE", "/api/auth/link", ""},
	} {
		code, _ := status(t, stranger, tc.method, f.srv.URL+tc.path, nil, tc.body)
		if code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("%s %s as a stranger = %d, want 401 or 403", tc.method, tc.path, code)
		}
	}
}

// Setup must not be a way to bypass the cross-origin check either.
func TestSetupRefusesACrossOriginRequest(t *testing.T) {
	f := newAccountFixture(t, "")
	code, _ := status(t, f.client, "POST", f.srv.URL+"/api/auth/setup",
		map[string]string{"Origin": "https://evil.example"}, `{"password":"`+goodPassword+`"}`)
	if code != http.StatusForbidden {
		t.Errorf("cross-origin setup = %d, want 403", code)
	}
	if !f.account.SetupRequired() {
		t.Error("a cross-origin request claimed the account")
	}
}

func bcryptOf(t *testing.T, password string) string {
	t.Helper()
	// MinCost: these tests hash on every run and none of them are measuring
	// how slow bcrypt is.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return string(hash)
}

// With a provider configured, an anonymous stranger must not be able to claim
// the built-in account: that would be taking an account which bypasses the
// provider, rather than bootstrapping the only way in.
func TestSetupRequiresASessionWhenAProviderCanLetYouIn(t *testing.T) {
	f := newAccountFixture(t, "", func(g *api.Gate) {
		proxy, err := auth.NewProxy(true, "X-Remote-User", nil)
		if err != nil {
			t.Fatalf("NewProxy: %v", err)
		}
		g.Proxy = proxy
	})

	code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("anonymous setup = %d %s, want 401", code, body)
	}
	if !f.account.SetupRequired() {
		t.Fatal("an anonymous request claimed the account despite a proxy being configured")
	}

	// Signed in the way the install is set up to do, it works.
	code, body = status(t, f.client, "POST", f.srv.URL+"/api/auth/setup",
		map[string]string{"X-Remote-User": "andri"}, `{"password":"`+goodPassword+`"}`)
	if code != 200 {
		t.Fatalf("setup while signed in = %d %s", code, body)
	}
	if f.account.SetupRequired() {
		t.Error("the account is still unclaimed")
	}
}

// And with nothing else configured it stays anonymous, or a fresh install
// would have no way in at all.
func TestSetupStaysAnonymousWhenItIsTheOnlyWayIn(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("setup = %d %s, want 200", code, body)
	}
}

// The login screen picks its shape from these two, so they have to disagree in
// exactly the case that matters.
func TestSetupOnlyIsFalseWhenAProviderExists(t *testing.T) {
	bare := newAccountFixture(t, "")
	state := bare.state(t)
	if state["setup_required"] != true || state["setup_only"] != true {
		t.Errorf("with nothing else configured: %v, want both true", state)
	}

	withProxy := newAccountFixture(t, "", func(g *api.Gate) {
		proxy, err := auth.NewProxy(true, "X-Remote-User", nil)
		if err != nil {
			t.Fatalf("NewProxy: %v", err)
		}
		g.Proxy = proxy
	})
	state = withProxy.state(t)
	if state["setup_required"] != true || state["setup_only"] != false {
		t.Errorf("with a proxy configured: %v, want setup_required without setup_only", state)
	}
}

// Linking the built-in account to a provider identity had no test at all,
// which is a gap worth closing: a linked subject *is* the built-in account, so
// this is the one place where a string from an identity provider grants
// administrator rights by matching a stored value.

func TestALinkedSubjectIsTheBuiltInAccount(t *testing.T) {
	f := newAccountFixture(t, "")
	ctx := context.Background()
	if err := f.account.Claim(ctx, goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if f.account.LinkedTo("") {
		t.Error("an unlinked account matched the empty subject; every anonymous claim would be an administrator")
	}
	if f.account.LinkedTo("alice@example.test") {
		t.Error("an unlinked account matched a subject")
	}

	if err := f.account.Link(ctx, "alice@example.test"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if !f.account.LinkedTo("alice@example.test") {
		t.Error("the linked subject does not match")
	}
	if f.account.LinkedTo("mallory@example.test") {
		t.Error("a different subject matched the link")
	}
	if f.account.LinkedTo("") {
		t.Error("the empty subject matched a link; a provider that returns no subject would sign in as the administrator")
	}

	// Unlinking is passing nothing, and must not leave "" matching.
	if err := f.account.Link(ctx, ""); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if f.account.LinkedTo("alice@example.test") || f.account.LinkedTo("") {
		t.Error("the link survived being removed")
	}
}

func TestTheLinkSurvivesAReload(t *testing.T) {
	// The in-memory copy and the row have to agree, or a restart would forget
	// the link and lock someone out of the account they sign in to with SSO.
	f := newAccountFixture(t, "")
	ctx := context.Background()
	if err := f.account.Claim(ctx, goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := f.account.Link(ctx, "alice@example.test"); err != nil {
		t.Fatalf("link: %v", err)
	}

	reloaded, err := auth.LoadAccount(ctx, f.db, "", true)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.LinkedTo("alice@example.test") {
		t.Error("the link was not read back from the database")
	}
}

func TestUnlinkingNeedsTheBuiltInAccount(t *testing.T) {
	f := newAccountFixture(t, "")
	ctx := context.Background()
	if err := f.account.Claim(ctx, goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := f.account.Link(ctx, "alice@example.test"); err != nil {
		t.Fatalf("link: %v", err)
	}

	// Not signed in at all: refused by the middleware before the handler ever
	// runs, so this is 401 rather than the handler's own 403.
	if code, _ := f.do(t, "DELETE", "/api/auth/link", ""); code != http.StatusUnauthorized {
		t.Errorf("an anonymous unlink = %d, want 401", code)
	}
	if !f.account.LinkedTo("alice@example.test") {
		t.Fatal("the link was removed by a request that should not have been allowed")
	}

	if code, body := f.do(t, "POST", "/api/login", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("login = %d %s", code, body)
	}
	if code, body := f.do(t, "DELETE", "/api/auth/link", ""); code != 200 {
		t.Fatalf("unlink = %d %s", code, body)
	}
	if f.account.LinkedTo("alice@example.test") {
		t.Error("the link is still there after unlinking")
	}
}
