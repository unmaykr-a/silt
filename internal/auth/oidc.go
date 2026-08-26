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
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("SILT_OIDC_ISSUER is set but no redirect URL could be determined; set SILT_OIDC_REDIRECT_URL or SILT_BASE_URL")
	}
	if _, err := url.Parse(cfg.RedirectURL); err != nil {
		return nil, fmt.Errorf("SILT_OIDC_REDIRECT_URL %q is not a valid URL: %w", cfg.RedirectURL, err)
	}

	discovery, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(discovery, strings.TrimRight(cfg.Issuer, "/"))
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
func (o *OIDC) Start(next string, link bool) (authURL string, flow Flow, err error) {
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

	challenge := sha256.Sum256([]byte(verifier))
	url := o.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return url, Flow{
		State:    state,
		Nonce:    nonce,
		Verifier: verifier,
		Next:     SafeNext(next),
		Link:     link,
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
	return Identity{Subject: claims.Subject, Name: claims.Display(), Method: MethodOIDC}, nil
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

	token, err := o.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", flow.Verifier))
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
