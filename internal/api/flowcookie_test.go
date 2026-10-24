package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/config"
)

// The cookie that carries a login's state and PKCE verifier used to decide its
// own Secure flag from the request, while the session cookie asked
// SILT_COOKIE_SECURE. So on the deployment that setting exists for -- a proxy
// terminating TLS without setting X-Forwarded-Proto -- Silt marked the session
// cookie Secure and sent the login state in the clear.

// fakeIssuer serves just enough discovery for NewOIDC to succeed. Starting a
// login is a redirect; nothing here is exchanged, so no keys are needed.
func fakeIssuer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	})
	return srv.URL
}

func withOIDCProvider(t *testing.T) fixtureOpt {
	t.Helper()
	issuer := fakeIssuer(t)
	return withGate(func(g *api.Gate) {
		client, err := auth.NewOIDC(t.Context(), auth.OIDCConfig{
			Issuer:       issuer,
			ClientID:     "silt",
			ClientSecret: "unused-by-a-redirect",
			RedirectURL:  "https://silt.example.lan/api/auth/callback",
		})
		if err != nil {
			t.Fatalf("NewOIDC: %v", err)
		}
		g.OIDC = client
	})
}

// startLogin asks for the provider redirect and returns the response, without
// following it.
func startLogin(t *testing.T, f *accountFixture) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/api/auth/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *f.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("start login: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("start login = %d, want a redirect to the provider", resp.StatusCode)
	}
	return resp
}

func flowCookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "silt_login" {
			return c
		}
	}
	t.Fatal("no login flow cookie was set")
	return nil
}

func TestTheLoginFlowCookieHonoursAlwaysSecure(t *testing.T) {
	// Plain HTTP with no forwarded header: exactly the shape the setting is for.
	f := newAccountFixture(t, "", withCookieSecure(config.CookieSecureAlways), withOIDCProvider(t))

	if c := flowCookieFrom(t, startLogin(t, f)); !c.Secure {
		t.Error("SILT_COOKIE_SECURE=always did not reach the login flow cookie")
	}
}

func TestTheLoginFlowCookieHonoursAnHTTPSBaseURL(t *testing.T) {
	f := newAccountFixture(t, "", withBaseURL("https://silt.example.lan"), withOIDCProvider(t))

	if c := flowCookieFrom(t, startLogin(t, f)); !c.Secure {
		t.Error("an https base URL did not reach the login flow cookie")
	}
}

func TestTheLoginFlowCookieStaysPlainUnderAuto(t *testing.T) {
	// The other direction, which matters just as much: a Secure cookie on a
	// plain-HTTP LAN install never comes back, and a login that cannot carry
	// its own state cannot complete.
	f := newAccountFixture(t, "", withCookieSecure(config.CookieSecureAuto), withOIDCProvider(t))

	c := flowCookieFrom(t, startLogin(t, f))
	if c.Secure {
		t.Error("auto set Secure on the flow cookie over plain HTTP")
	}
	if !c.HttpOnly {
		t.Error("the flow cookie is readable from script")
	}
	if c.SameSite != http.SameSiteLaxMode {
		// Strict would drop it on the way back from the provider, which is a
		// top-level cross-site navigation.
		t.Errorf("flow cookie SameSite = %v, want Lax", c.SameSite)
	}
}
