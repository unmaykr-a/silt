package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/unmaykr-a/silt/internal/auth"
)

// fakeProvider is enough of an OpenID Connect provider to exercise the whole
// flow: discovery, a token endpoint, and a JWKS to verify against.
//
// A real signature over a real JWKS, rather than a stub that returns a canned
// identity — the point of these tests is the verification, and a fake that
// skips it would prove nothing.
type fakeProvider struct {
	*httptest.Server
	// key is what the JWKS advertises.
	key *rsa.PrivateKey
	// signKey is what tokens are actually signed with. Set it to something
	// else to produce a token the JWKS cannot verify — which is what an
	// attacker presenting their own token looks like.
	signKey *rsa.PrivateKey
	// claims is what the next id_token will carry, beyond iss/aud/exp.
	claims map[string]any
	// nonce is echoed into the id_token. Overridden to test the mismatch.
	nonce string
	// lastForm records what the client sent to the token endpoint.
	lastForm map[string]string
	// omitIDToken drops the id_token from the response.
	omitIDToken bool
	// issuer overrides what the discovery document reports. authentik reports
	// its issuer with a trailing slash, which is also how it prints it for you
	// to copy.
	issuer string
}

// declaredIssuer is what the discovery document says, which is what go-oidc
// compares against the string Silt passed it.
func (p *fakeProvider) declaredIssuer() string {
	if p.issuer != "" {
		return p.issuer
	}
	return p.URL
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p := &fakeProvider{key: key, claims: map[string]any{}, lastForm: map[string]string{}}

	mux := http.NewServeMux()
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 p.declaredIssuer(),
			"authorization_endpoint": p.URL + "/authorize",
			"token_endpoint":         p.URL + "/token",
			"jwks_uri":               p.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       p.key.Public(),
			KeyID:     "test",
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		for key := range r.Form {
			p.lastForm[key] = r.Form.Get(key)
		}
		body := map[string]any{"access_token": "at", "token_type": "Bearer"}
		if !p.omitIDToken {
			body["id_token"] = p.sign(t)
		}
		writeJSON(w, body)
	})
	return p
}

func (p *fakeProvider) sign(t *testing.T) string {
	t.Helper()
	signing := p.signKey
	if signing == nil {
		signing = p.key
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: signing},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"),
	)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	claims := map[string]any{
		"iss":   p.declaredIssuer(),
		"aud":   "silt",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"sub":   "user-1",
		"nonce": p.nonce,
	}
	for k, v := range p.claims {
		claims[k] = v
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newOIDC(t *testing.T, p *fakeProvider, mutate func(*auth.OIDCConfig)) *auth.OIDC {
	t.Helper()
	cfg := auth.OIDCConfig{
		Issuer:      p.URL,
		ClientID:    "silt",
		RedirectURL: "https://silt.example/api/auth/callback",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	o, err := auth.NewOIDC(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	return o
}

// login drives a whole flow and returns what Silt made of it.
func login(t *testing.T, o *auth.OIDC, p *fakeProvider, next string) (auth.Identity, error) {
	t.Helper()
	_, flow, err := o.Start(next, false, "https://silt.example")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.nonce = flow.Nonce
	return o.Finish(context.Background(), flow, flow.State, "the-code")
}

func TestOIDCHappyPath(t *testing.T) {
	p := newFakeProvider(t)
	p.claims["preferred_username"] = "andri"
	o := newOIDC(t, p, nil)

	id, err := login(t, o, p, "/projects/3")
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if id.Subject != "user-1" || id.Name != "andri" || id.Method != auth.MethodOIDC {
		t.Errorf("identity = %+v, want the provider's subject and username", id)
	}
}

// PKCE is the reason a leaked authorization code is not enough on its own.
func TestOIDCSendsAPKCEChallengeAndVerifier(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)

	authURL, flow, err := o.Start("/", false, "https://silt.example")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.Contains(authURL, "code_challenge=") {
		t.Error("the authorization URL carries no PKCE challenge")
	}
	if !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Error("the PKCE method is not S256")
	}
	if strings.Contains(authURL, flow.Verifier) {
		t.Fatal("the code verifier itself was put in the authorization URL")
	}

	p.nonce = flow.Nonce
	if _, err := o.Finish(context.Background(), flow, flow.State, "code"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if p.lastForm["code_verifier"] != flow.Verifier {
		t.Error("the code verifier was not sent to the token endpoint")
	}
}

// The state parameter is what ties a callback to a login Silt started.
func TestOIDCRefusesAStateMismatch(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)
	_, flow, _ := o.Start("/", false, "https://silt.example")
	p.nonce = flow.Nonce

	for _, state := range []string{"", "somebody-elses-state", flow.State + "x"} {
		if _, err := o.Finish(context.Background(), flow, state, "code"); err == nil {
			t.Errorf("state %q was accepted", state)
		}
	}
	if len(p.lastForm) != 0 {
		t.Error("a mismatched state still reached the token endpoint")
	}
}

// The nonce is what stops an id_token obtained elsewhere being replayed here.
func TestOIDCRefusesANonceMismatch(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)
	_, flow, _ := o.Start("/", false, "https://silt.example")
	p.nonce = "a-different-login"

	_, err := o.Finish(context.Background(), flow, flow.State, "code")
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Errorf("error = %v, want a nonce mismatch", err)
	}
}

func TestOIDCRefusesATokenSignedByTheWrongKey(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)
	_, flow, _ := o.Start("/", false, "https://silt.example")
	p.nonce = flow.Nonce

	// Sign with a key the JWKS does not advertise: exactly what an attacker
	// presenting their own token looks like.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p.signKey = other

	if _, err := o.Finish(context.Background(), flow, flow.State, "code"); err == nil {
		t.Error("a token signed by an unknown key was accepted")
	}
}

func TestOIDCRefusesAMissingIDToken(t *testing.T) {
	p := newFakeProvider(t)
	p.omitIDToken = true
	o := newOIDC(t, p, nil)
	_, flow, _ := o.Start("/", false, "https://silt.example")

	if _, err := o.Finish(context.Background(), flow, flow.State, "code"); err == nil {
		t.Error("a token response with no id_token was accepted")
	}
}

func TestOIDCGroupAllowlist(t *testing.T) {
	cases := []struct {
		name    string
		groups  any
		allowed []string
		want    bool
	}{
		{"member of an allowed group", []any{"admins", "users"}, []string{"admins"}, true},
		{"case is not significant", []any{"Admins"}, []string{"admins"}, true},
		{"not a member", []any{"users"}, []string{"admins"}, false},
		{"no groups at all", nil, []string{"admins"}, false},
		{"a single string rather than a list", "admins", []string{"admins"}, true},
		{"a JSON-encoded list in a string", `["admins","users"]`, []string{"admins"}, true},
		{"an empty allowlist admits anyone", []any{"nobody"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newFakeProvider(t)
			if tc.groups != nil {
				p.claims["groups"] = tc.groups
			}
			o := newOIDC(t, p, func(c *auth.OIDCConfig) { c.AllowedGroups = tc.allowed })

			_, err := login(t, o, p, "/")
			if tc.want && err != nil {
				t.Errorf("login refused: %v", err)
			}
			if !tc.want {
				var refused *auth.ErrNotAllowed
				if err == nil {
					t.Error("login was allowed")
				} else if !errorsAs(err, &refused) {
					t.Errorf("error = %v, want ErrNotAllowed", err)
				}
			}
		})
	}
}

func TestOIDCUserAllowlistMatchesUsernameEmailOrSubject(t *testing.T) {
	for _, allowed := range []string{"andri", "andri@example.com", "user-1"} {
		p := newFakeProvider(t)
		p.claims["preferred_username"] = "andri"
		p.claims["email"] = "andri@example.com"
		o := newOIDC(t, p, func(c *auth.OIDCConfig) {
			// A group list that matches nothing, so only the user list can
			// admit: this is testing the user list, not falling through.
			c.AllowedGroups = []string{"nobody"}
			c.AllowedUsers = []string{allowed}
		})
		if _, err := login(t, o, p, "/"); err != nil {
			t.Errorf("allowed user %q was refused: %v", allowed, err)
		}
	}
}

// Providers disagree about where the username and groups live, so neither
// claim name is hard-coded.
func TestOIDCHonoursCustomClaimNames(t *testing.T) {
	p := newFakeProvider(t)
	p.claims["silt_user"] = "andri"
	p.claims["roles"] = []any{"silt-admin"}
	o := newOIDC(t, p, func(c *auth.OIDCConfig) {
		c.UsernameClaim = "silt_user"
		c.GroupsClaim = "roles"
		c.AllowedGroups = []string{"silt-admin"}
	})

	id, err := login(t, o, p, "/")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if id.Name != "andri" {
		t.Errorf("name = %q, want the custom username claim", id.Name)
	}
}

func TestOIDCFallsBackThroughTheDisplayNames(t *testing.T) {
	p := newFakeProvider(t)
	p.claims["email"] = "andri@example.com"
	o := newOIDC(t, p, nil)
	id, err := login(t, o, p, "/")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if id.Name != "andri@example.com" {
		t.Errorf("name = %q, want the email when there is no username claim", id.Name)
	}
}

// The post-login destination goes through the same reduction as everything
// else, so the login flow cannot become an open redirect.
func TestOIDCStartSanitisesTheReturnPath(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)
	_, flow, err := o.Start("https://evil.example/", false, "https://silt.example")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if flow.Next != "/" {
		t.Errorf("next = %q, want /", flow.Next)
	}
}

func TestOIDCConfigurationErrors(t *testing.T) {
	ctx := context.Background()
	// No issuer is not an error: it means "not configured".
	if o, err := auth.NewOIDC(ctx, auth.OIDCConfig{}); err != nil || o.Enabled() {
		t.Errorf("empty config = (%v, %v), want a disabled provider and no error", o, err)
	}
	if _, err := auth.NewOIDC(ctx, auth.OIDCConfig{Issuer: "https://x"}); err == nil {
		t.Error("an issuer with no client ID was accepted")
	}
	if _, err := auth.NewOIDC(ctx, auth.OIDCConfig{Issuer: "https://x", ClientID: "silt"}); err == nil {
		t.Error("an issuer with no redirect URL was accepted")
	}
}

func TestDisabledOIDCIsSafeToCall(t *testing.T) {
	var o *auth.OIDC
	if o.Enabled() {
		t.Error("a nil provider reports itself enabled")
	}
	if o.Issuer() != "" {
		t.Error("a nil provider returned an issuer")
	}
}

// errorsAs is errors.As, spelled out to keep the import list of this test file
// about the thing under test.
func errorsAs(err error, target **auth.ErrNotAllowed) bool {
	for err != nil {
		if e, ok := err.(*auth.ErrNotAllowed); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// authentik publishes its issuer with a trailing slash, and prints it that way
// for you to copy into the client. go-oidc compares the issuer in the
// discovery document against the string it was given character for character,
// so normalising it turned "paste the URL your provider shows you" into a
// silent discovery failure and a login button that never appeared.
func TestOIDCAcceptsAnIssuerWithATrailingSlash(t *testing.T) {
	p := newFakeProvider(t)
	p.issuer = p.URL + "/"
	p.claims["preferred_username"] = "andri"

	o, err := auth.NewOIDC(context.Background(), auth.OIDCConfig{
		Issuer:      p.URL + "/",
		ClientID:    "silt",
		RedirectURL: "https://silt.example/api/auth/callback",
	})
	if err != nil {
		t.Fatalf("NewOIDC with a trailing-slash issuer: %v", err)
	}
	if !o.Enabled() {
		t.Fatal("the provider is not enabled")
	}
	id, err := login(t, o, p, "/")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if id.Name != "andri" {
		t.Errorf("name = %q, want andri", id.Name)
	}
}

// With no redirect URL configured, the callback is derived from the request —
// so a working install does not need SILT_BASE_URL set before it can start.
func TestOIDCDerivesTheCallbackFromTheRequest(t *testing.T) {
	p := newFakeProvider(t)
	o, err := auth.NewOIDC(context.Background(), auth.OIDCConfig{
		Issuer:   p.URL,
		ClientID: "silt",
	})
	if err != nil {
		t.Fatalf("NewOIDC without a redirect URL: %v", err)
	}
	if o.Configured() {
		t.Error("the callback reports itself as pinned when nothing was configured")
	}

	authURL, flow, err := o.Start("/", false, "https://silt.example.lan")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	const want = "https://silt.example.lan/api/auth/callback"
	if flow.Redirect != want {
		t.Errorf("derived callback = %q, want %q", flow.Redirect, want)
	}
	if !strings.Contains(authURL, url.QueryEscape(want)) {
		t.Errorf("the authorization URL does not carry the derived callback: %s", authURL)
	}

	// OAuth requires the exchange to repeat the same redirect_uri.
	p.nonce = flow.Nonce
	if _, err := o.Finish(context.Background(), flow, flow.State, "code"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if p.lastForm["redirect_uri"] != want {
		t.Errorf("exchange sent redirect_uri %q, want %q", p.lastForm["redirect_uri"], want)
	}
}

// A configured redirect URL wins, because it is what was registered with the
// provider and a request's Host is not.
func TestConfiguredRedirectBeatsTheRequest(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)
	if !o.Configured() {
		t.Error("a configured callback does not report itself as pinned")
	}
	_, flow, err := o.Start("/", false, "https://someone-elses-host.example")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if flow.Redirect != "https://silt.example/api/auth/callback" {
		t.Errorf("callback = %q; a request Host overrode the configured URL", flow.Redirect)
	}
}

func TestStartWithoutAnyCallbackIsAnError(t *testing.T) {
	p := newFakeProvider(t)
	o, err := auth.NewOIDC(context.Background(), auth.OIDCConfig{Issuer: p.URL, ClientID: "silt"})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	if _, _, err := o.Start("/", false, ""); err == nil {
		t.Error("Start succeeded with no configured and no derivable callback")
	}
}
