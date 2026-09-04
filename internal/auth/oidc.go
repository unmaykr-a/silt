package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig is everything an OpenID Connect login needs.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL must match what is registered with the provider exactly.
	RedirectURL string
	Scopes      []string
	// UsernameClaim and GroupsClaim differ between providers: authentik and
	// Keycloak put groups in `groups`, others in `roles` or a namespaced
	// claim, so neither is hard-coded.
	UsernameClaim string
	GroupsClaim   string
	// AllowedGroups and AllowedUsers restrict who may sign in. Both empty
	// means anyone the provider authenticates, which is the right default only
	// because the provider is doing the deciding.
	AllowedGroups []string
	AllowedUsers  []string
	// AdminGroups splits reading from administering. Empty means everyone
	// admitted is an administrator, which is what Silt did before roles
	// existed.
	AdminGroups []string
}

// OIDC is a configured provider, ready to start and finish a login.
type OIDC struct {
	cfg      OIDCConfig
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDC discovers the provider's endpoints and returns a usable client.
//
// Discovery happens once at startup rather than per login: a provider that is
// down should be a startup warning, not a login that hangs. It is also the
// only place a typo'd issuer surfaces before someone tries to sign in.
func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDC, error) {
	if cfg.Issuer == "" {
		return nil, nil
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("SILT_OIDC_ISSUER is set but SILT_OIDC_CLIENT_ID is empty")
	}
	if cfg.RedirectURL != "" {
		if _, err := url.Parse(cfg.RedirectURL); err != nil {
			return nil, fmt.Errorf("SILT_OIDC_REDIRECT_URL %q is not a valid URL: %w", cfg.RedirectURL, err)
		}
	}

	discovery, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// The issuer goes to the library exactly as configured, trailing slash and
	// all. go-oidc compares the issuer in the discovery document against the
	// string it was given, character for character — and authentik publishes
	// its issuer with a trailing slash, which is also how it prints it for you
	// to copy. Normalising it here turned "paste the URL authentik shows you"
	// into a silent discovery failure.
	provider, err := oidc.NewProvider(discovery, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OpenID Connect provider at %s: %w", cfg.Issuer, err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}

	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}

	return &OIDC{
		cfg:      cfg,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
	}, nil
}

// Enabled reports whether OIDC is configured.
func (o *OIDC) Enabled() bool { return o != nil }

// Issuer is shown on the login screen, so someone knows which provider the
// button will send them to.
func (o *OIDC) Issuer() string {
	if o == nil {
		return ""
	}
	return o.cfg.Issuer
}

// Flow is the per-login state that has to survive the round trip to the
// provider. It is handed to the browser in a short-lived cookie, never stored
// server-side: there is exactly one consumer and it arrives with the callback.
type Flow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	// Next is where to send the browser afterwards. Validated as a
	// same-origin path before it is ever used, so it cannot become an open
	// redirect through the login flow.
	Next string `json:"r"`
	// Redirect is the callback URL that was sent to the provider. OAuth
	// requires the exchange to repeat it exactly, and it can be derived from
	// the request rather than configured, so it is carried rather than
	// recomputed.
	Redirect string `json:"u,omitempty"`
	// Link marks a round trip whose purpose is to record which provider
	// identity belongs to the local account, rather than to sign anyone in.
	Link bool `json:"l,omitempty"`
}

// Start begins a login and returns the URL to send the browser to.
//
// PKCE is used even though this is a confidential client with a secret. It
// costs one hash and closes the case where the authorization code leaks — a
// proxy log, a Referer header, a shared machine's history — before the
// exchange happens.
// redirect is the callback URL to use, preferring what was configured.
//
// Deriving it from the request when nothing is configured is safe: the
// provider only honours redirect URIs registered with it, so a request
// carrying a forged Host produces a URL the provider refuses rather than one
// it redirects to.
func (o *OIDC) redirect(requestBase string) string {
	if o.cfg.RedirectURL != "" {
		return o.cfg.RedirectURL
	}
	if requestBase == "" {
		return ""
	}
	return strings.TrimRight(requestBase, "/") + "/api/auth/callback"
}

// Configured reports whether the callback URL is pinned rather than derived.
func (o *OIDC) Configured() bool { return o != nil && o.cfg.RedirectURL != "" }

func (o *OIDC) Start(next string, link bool, requestBase string) (authURL string, flow Flow, err error) {
	state, err := randomString()
	if err != nil {
		return "", Flow{}, err
	}
	nonce, err := randomString()
	if err != nil {
		return "", Flow{}, err
	}
	verifier, err := randomString()
	if err != nil {
		return "", Flow{}, err
	}

	callback := o.redirect(requestBase)
	if callback == "" {
		return "", Flow{}, fmt.Errorf("no callback URL: set SILT_BASE_URL or SILT_OIDC_REDIRECT_URL")
	}

	challenge := sha256.Sum256([]byte(verifier))
	url := o.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("redirect_uri", callback),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return url, Flow{
		State:    state,
		Nonce:    nonce,
		Verifier: verifier,
		Next:     SafeNext(next),
		Link:     link,
		Redirect: callback,
	}, nil
}

// Claims is what Silt reads out of an ID token.
type Claims struct {
	Subject  string
	Username string
	Email    string
	Name     string
	Groups   []string
}

// ErrNotAllowed means the provider authenticated someone Silt will not admit.
type ErrNotAllowed struct{ Subject string }

func (e *ErrNotAllowed) Error() string {
	return fmt.Sprintf("%s is not in an allowed group", e.Subject)
}

// Finish exchanges the code and returns the identity, or refuses it.
func (o *OIDC) Finish(ctx context.Context, flow Flow, state, code string) (Identity, error) {
	claims, err := o.Exchange(ctx, flow, state, code)
	if err != nil {
		return Identity{}, err
	}
	if !o.Allowed(claims) {
		return Identity{}, &ErrNotAllowed{Subject: claims.Display()}
	}
	return Identity{
		Subject: claims.Subject,
		Name:    claims.Display(),
		Method:  MethodOIDC,
		Role:    o.RoleFor(claims),
	}, nil
}

// Exchange completes the round trip and returns the verified claims, without
// applying the allowlists.
//
// Separate from Finish because two callers want different things from the same
// exchange: a login has to be refused when the allowlists say so, and linking
// an account only needs to know which identity just proved itself.
func (o *OIDC) Exchange(ctx context.Context, flow Flow, state, code string) (Claims, error) {
	// Compared before anything is spent on a network round trip: a mismatched
	// state means this callback did not come from the login Silt started.
	if state == "" || state != flow.State {
		return Claims{}, fmt.Errorf("state mismatch; the login did not start here")
	}

	options := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("code_verifier", flow.Verifier)}
	if flow.Redirect != "" {
		// OAuth requires the exchange to repeat the redirect_uri the
		// authorization request used, and that one may have been derived from
		// the request rather than configured.
		options = append(options, oauth2.SetAuthURLParam("redirect_uri", flow.Redirect))
	}
	token, err := o.oauth.Exchange(ctx, code, options...)
	if err != nil {
		return Claims{}, fmt.Errorf("exchange authorization code: %w", err)
	}

	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Claims{}, fmt.Errorf("provider returned no id_token")
	}
	idToken, err := o.verifier.Verify(ctx, rawID)
	if err != nil {
		return Claims{}, fmt.Errorf("verify id_token: %w", err)
	}
	// The nonce binds this token to the login Silt started, which is what stops
	// a token obtained elsewhere from being replayed into this session.
	if idToken.Nonce != flow.Nonce {
		return Claims{}, fmt.Errorf("nonce mismatch; the id_token belongs to a different login")
	}

	var raw map[string]any
	if err := idToken.Claims(&raw); err != nil {
		return Claims{}, fmt.Errorf("read claims: %w", err)
	}
	return o.extract(idToken.Subject, raw), nil
}

// Display is the friendliest name the provider offered.
func (c Claims) Display() string {
	for _, candidate := range []string{c.Username, c.Email, c.Name, c.Subject} {
		if candidate != "" {
			return candidate
		}
	}
	return "unknown"
}

func (o *OIDC) extract(subject string, raw map[string]any) Claims {
	c := Claims{Subject: subject}
	c.Username = stringClaim(raw[o.cfg.UsernameClaim])
	c.Email = stringClaim(raw["email"])
	c.Name = stringClaim(raw["name"])
	c.Groups = stringsClaim(raw[o.cfg.GroupsClaim])
	return c
}

// Allowed applies the group and user lists. Both empty admits anyone the
// provider authenticated, which is a deliberate default: the point of pointing
// Silt at a provider is to let the provider decide.
func (o *OIDC) Allowed(c Claims) bool {
	// A disabled provider admits nobody. Nothing reaches this with a nil
	// receiver today, because the callback is only routed when OIDC is on —
	// but Enabled() is explicitly nil-safe, so the rest of the type should be,
	// and the safe answer for "is this person allowed" is no.
	if o == nil {
		return false
	}
	if len(o.cfg.AllowedGroups) == 0 && len(o.cfg.AllowedUsers) == 0 {
		return true
	}
	for _, want := range o.cfg.AllowedUsers {
		if equalFold(want, c.Username) || equalFold(want, c.Email) || want == c.Subject {
			return true
		}
	}
	for _, want := range o.cfg.AllowedGroups {
		for _, has := range c.Groups {
			if equalFold(want, has) {
				return true
			}
		}
	}
	return false
}

// RoleFor decides what an admitted identity may do.
//
// No admin group configured means everyone admitted is an administrator: that
// is what Silt did before roles existed, and turning an upgrade into a
// lockout for the person who configured it would be the worst possible
// default.
func (o *OIDC) RoleFor(c Claims) Role {
	if o == nil {
		return RoleViewer
	}
	return RoleFromGroups(o.cfg.AdminGroups, c.Groups)
}

// IdentityFor decides which identity a set of verified provider claims signs in
// as, and whether they sign in at all.
//
// Two separate questions, in this order, because conflating them was a bug in
// both directions.
//
// The allowlist decides whether you get in. The link decides which account you
// land in once you are. The callback used to check the link first and on its
// own, so a linked subject skipped the allowlist entirely — removing that
// person from the permitted group in the provider left them still signing in,
// as the administrator, which is the opposite of what removing them meant.
//
// And the role has to be read from the claims here. The callback used to build
// the Identity by hand and leave Role unset, which ParseRole reads as
// administrator: SILT_OIDC_ADMIN_GROUPS was configured, reported on the
// settings screen, and did nothing at all. That is why this lives beside the
// rules it applies rather than in the handler — there is one way to turn claims
// into an identity, and it is this.
func IdentityFor(o *OIDC, account *Account, claims Claims) (Identity, bool) {
	if !o.Allowed(claims) {
		return Identity{}, false
	}
	id := Identity{
		Subject: claims.Subject,
		Name:    claims.Display(),
		Method:  MethodOIDC,
		Role:    o.RoleFor(claims),
	}
	// The link stays more specific than a group — an explicit statement about
	// one identity should beat a membership rule — and the built-in account is
	// the operator's own, so reaching it is administrator access whatever the
	// group rules would have said about the provider identity that got here.
	if account.LinkedTo(claims.Subject) {
		id.Subject = LocalSubject
		id.Role = RoleAdmin
	}
	return id, true
}

// RoleFromGroups is the rule itself, shared with forward auth: the same split
// should not be spelled differently depending on which proxy asserted it.
func RoleFromGroups(adminGroups, has []string) Role {
	if len(adminGroups) == 0 {
		return RoleAdmin
	}
	for _, want := range adminGroups {
		for _, group := range has {
			if equalFold(want, group) {
				return RoleAdmin
			}
		}
	}
	return RoleViewer
}

func equalFold(a, b string) bool {
	return b != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func stringClaim(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// stringsClaim copes with the shapes providers actually send: a list, a single
// string, or a JSON-encoded list in a string.
func stringsClaim(v any) []string {
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s := stringClaim(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, "[") {
			var list []string
			if json.Unmarshal([]byte(trimmed), &list) == nil {
				return list
			}
		}
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	default:
		return nil
	}
}

// SafeNext reduces a post-login destination to a same-origin path.
//
// Anything else is dropped to "/". A login flow that will redirect anywhere it
// is told is an open redirect, and an open redirect on the login endpoint is
// the classic way to make a phishing link look like it came from the real
// site.
func SafeNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") {
		return "/"
	}
	// "//host" and "/\host" are scheme-relative URLs, not paths.
	if strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return parsed.RequestURI()
}

func randomString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
