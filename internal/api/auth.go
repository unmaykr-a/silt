package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/store"
)

// The HTTP surface of authentication. The deciding lives in internal/auth;
// this is the part that reads cookies, writes them, and refuses requests.

const (
	sessionCookie = "silt_session"
	flowCookie    = "silt_login"
	// A login round trip is a redirect to a provider and back. Ten minutes is
	// generous for someone who has to type a password and approve a prompt,
	// and short enough that an abandoned flow does not linger.
	flowMaxAge = 10 * time.Minute
)

// Gate is everything the server needs to decide who is asking.
type Gate struct {
	Sessions *auth.Sessions
	// Account is the built-in administrator. It supersedes Password, which
	// remains only for tests that want a bare verifier.
	Account  *auth.Account
	Password *auth.Password
	Proxy    *auth.Proxy
	OIDC     *auth.OIDC
	// OIDCError explains why a configured provider is not usable, so the login
	// screen can say so instead of quietly omitting the button. Silt that
	// looks like it has no provider, when you configured one, is worse than
	// Silt that says the provider is unreachable.
	OIDCError string
	// MetricsPublic leaves /metrics reachable without authentication.
	MetricsPublic bool
	// AllowedOrigins are extra origins accepted on unsafe requests, beyond the
	// one the request was addressed to.
	AllowedOrigins []string
}

// Enabled reports whether Silt refuses anonymous requests.
//
// An unclaimed local account counts. It has no password yet, so nobody can
// sign in — which is exactly why the door has to stay shut: an open API behind
// a setup screen would make the setup screen decoration.
func (g *Gate) Enabled() bool {
	if g == nil {
		return false
	}
	return g.Proxy.Enabled() || g.passwordEnabled() || g.OIDC.Enabled() || g.Account.Active()
}

// passwordEnabled covers both the account and the bare verifier tests use.
func (g *Gate) passwordEnabled() bool {
	return g.Account.Enabled() || g.Password.Enabled()
}

// verifyPassword checks a password against whichever verifier is in play.
func (g *Gate) verifyPassword(client, password string) bool {
	if g.Account.Enabled() {
		return g.Account.Verify(client, password)
	}
	return g.Password.Verify(client, password)
}

func (g *Gate) throttled(client string) (bool, time.Duration) {
	if g.Account.Enabled() {
		return g.Account.Throttled(client)
	}
	return g.Password.Throttled(client)
}

// setupRequired reports that the first thing to do is choose a password.
func (g *Gate) setupRequired() bool {
	return g != nil && g.Account.SetupRequired()
}

// SetAuth installs the gate.
func (s *Server) SetAuth(g *Gate) { s.gate = g }

// identify returns who is asking, if anyone.
//
// Forward auth first, because a proxy that asserts an identity on every
// request needs no session; then the session cookie, which is what both the
// password and OIDC logins produce.
func (s *Server) identify(r *http.Request) (auth.Identity, bool) {
	if !s.gate.Enabled() {
		return auth.Identity{}, true
	}
	if id, ok := s.gate.Proxy.Identify(r); ok {
		return id, true
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Identity{}, false
	}
	id, err := s.gate.Sessions.Lookup(r.Context(), cookie.Value)
	if err != nil {
		return auth.Identity{}, false
	}
	return id, true
}

// isPublic reports whether a path is reachable without authentication.
//
// Health probes must answer for orchestrators that cannot present
// credentials, /api/ingest carries its own token, and the login endpoints have
// to be reachable in order to log in at all. /metrics is no longer on this
// list by default: it names every project on the host.
func (s *Server) isPublic(path string) bool {
	switch path {
	case "/healthz", "/readyz",
		"/api/login", "/api/logout", "/api/auth", "/api/auth/setup",
		"/api/auth/login", "/api/auth/callback",
		"/api/ingest":
		return true
	case "/metrics":
		return s.gate != nil && s.gate.MetricsPublic
	default:
		return false
	}
}

// requireAuth wraps the route tree.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The CSRF check runs whether or not authentication is on, and before
		// it: an open install still has state-changing endpoints, and a page
		// on another origin should not be able to drive them through someone's
		// browser.
		if auth.CrossSite(r, s.gate.origins()) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if !s.gate.Enabled() || s.isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := s.identify(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		// The SPA needs to load in order to show a login form, so an
		// unauthenticated browser navigation gets the app; only API calls and
		// the metrics endpoint are refused outright.
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/metrics" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gate) origins() []string {
	if g == nil {
		return nil
	}
	return g.AllowedOrigins
}

// setSessionCookie writes the session cookie for this request's scheme.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Set only when the request actually arrived over TLS, directly or
		// through a proxy that says so. Setting it unconditionally would break
		// the common LAN install over plain HTTP — a cookie that never arrives
		// is worse than one relying on the operator's own network boundary —
		// and never setting it would waste the protection where it is
		// available.
		Secure: auth.SecureRequest(r),
		MaxAge: int(maxAge.Seconds()),
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   auth.SecureRequest(r),
		MaxAge:   -1,
	})
}

// clientKey identifies a login attempt's source for throttling.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// authStateResponse tells the UI what it is dealing with.
type authStateResponse struct {
	Required        bool   `json:"required"`
	PasswordEnabled bool   `json:"password_enabled"`
	OIDCEnabled     bool   `json:"oidc_enabled"`
	OIDCIssuer      string `json:"oidc_issuer,omitempty"`
	ProxyEnabled    bool   `json:"proxy_enabled"`
	// OIDCError is set when a provider is configured but unusable.
	OIDCError     string `json:"oidc_error,omitempty"`
	Authenticated bool   `json:"authenticated"`
	Subject       string `json:"subject,omitempty"`
	Method        string `json:"method,omitempty"`
	// SetupRequired means the built-in account exists but has no password.
	SetupRequired bool `json:"setup_required"`
	// SetupOnly means it is also the only way in, so the login screen should
	// offer nothing but choosing a password. With a provider configured the
	// setup form belongs on the settings screen instead, behind a sign-in.
	SetupOnly bool `json:"setup_only"`
	// MinPasswordLen lets the form say the rule before it refuses.
	MinPasswordLen int `json:"min_password_length"`
	// The built-in account's state, for the Security screen.
	LocalAvailable bool `json:"local_available"`
	LocalEnabled   bool `json:"local_enabled"`
	LocalManaged   bool `json:"local_managed"`
	LocalLinked    bool `json:"local_linked"`
}

func (s *Server) getAuthState(w http.ResponseWriter, r *http.Request) {
	out := authStateResponse{
		Required:        s.gate.Enabled(),
		PasswordEnabled: s.gate != nil && s.gate.passwordEnabled(),
		OIDCEnabled:     s.gate != nil && s.gate.OIDC.Enabled(),
		ProxyEnabled:    s.gate != nil && s.gate.Proxy.Enabled(),
		SetupRequired:   s.gate.setupRequired(),
		SetupOnly:       s.gate.setupRequired() && !s.gate.otherWayIn(),
		MinPasswordLen:  auth.MinPasswordLength,
	}
	if s.gate != nil && s.gate.Account.Available() {
		out.LocalAvailable = true
		out.LocalEnabled = s.gate.Account.Active()
		out.LocalManaged = s.gate.Account.ManagedByEnvironment()
		out.LocalLinked = s.gate.Account.LinkedSubject() != ""
	}
	if s.gate != nil {
		out.OIDCIssuer = s.gate.OIDC.Issuer()
		out.OIDCError = s.gate.OIDCError
	}
	if id, ok := s.identify(r); ok && s.gate.Enabled() {
		out.Authenticated = true
		out.Subject = id.Name
		out.Method = string(id.Method)
	} else if !s.gate.Enabled() {
		out.Authenticated = true
	}
	writeJSON(w, http.StatusOK, out)
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.passwordEnabled() {
		if s.gate.setupRequired() {
			writeError(w, http.StatusConflict, "this Silt has not been set up yet; choose a password first")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "password login is not configured")
		return
	}

	client := clientKey(r)
	if blocked, wait := s.gate.throttled(client); blocked {
		// Told plainly rather than silently rejected: the owner who typed it
		// wrong needs to know to wait, and an attacker learns nothing they
		// could not measure anyway.
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests,
			"too many failed attempts; try again in "+wait.Round(time.Second).String())
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	if !s.gate.verifyPassword(client, req.Password) {
		s.log.Warn("failed login attempt", "remote", client)
		// Recorded before the refusal is written: a rejected sign-in is the
		// audit row anyone actually goes looking for.
		s.auditFailed(r, store.AuditSignInFailed, map[string]any{"method": "password"})
		writeError(w, http.StatusUnauthorized, "incorrect password")
		return
	}

	token, err := s.gate.Sessions.Issue(r.Context(), auth.Identity{
		Subject: auth.LocalSubject,
		Name:    "local",
		Method:  auth.MethodPassword,
	})
	if err != nil {
		s.log.Error("issue session", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	setSessionCookie(w, r, token, s.gate.Sessions.TTL)
	s.log.Info("signed in", "method", "password", "remote", client)
	s.audit(r, store.AuditSignIn, map[string]any{"method": "password"})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	// Revoked server-side, not just forgotten by the browser. A cookie the
	// client throws away is still a working credential to anyone who copied
	// it; a deleted row is not.
	if cookie, err := r.Cookie(sessionCookie); err == nil && s.gate != nil && s.gate.Sessions != nil {
		if err := s.gate.Sessions.Revoke(r.Context(), cookie.Value); err != nil {
			s.log.Error("revoke session", "error", err)
		}
	}
	clearCookie(w, r, sessionCookie)
	s.audit(r, store.AuditSignOut, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// getOIDCLogin starts the OpenID Connect flow.
func (s *Server) getOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.OIDC.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "OpenID Connect is not configured")
		return
	}

	url, flow, err := s.gate.OIDC.Start(r.URL.Query().Get("next"), false, requestBase(r))
	if err != nil {
		s.log.Error("start OIDC login", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start the login")
		return
	}

	if !s.writeFlowCookie(w, r, flow) {
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// writeFlowCookie stores the per-round-trip state on the browser.
//
// A cookie rather than server state: there is exactly one consumer and it
// arrives with the callback, so a table would buy nothing but rows to expire.
// SameSite=Lax is required, not incidental — the callback is a top-level
// cross-site navigation from the provider, and Strict would drop the cookie on
// arrival.
func (s *Server) writeFlowCookie(w http.ResponseWriter, r *http.Request, flow auth.Flow) bool {
	encoded, err := json.Marshal(flow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the login")
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookie,
		Value:    base64.RawURLEncoding.EncodeToString(encoded),
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   auth.SecureRequest(r),
		MaxAge:   int(flowMaxAge.Seconds()),
	})
	return true
}

// getOIDCCallback finishes the flow and starts a session.
func (s *Server) getOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.OIDC.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "OpenID Connect is not configured")
		return
	}
	// One use, whatever the outcome: a flow that failed should not be
	// retryable with the same state.
	defer clearFlowCookie(w, r)

	if reason := r.URL.Query().Get("error"); reason != "" {
		s.log.Warn("provider refused the login", "error", reason,
			"description", r.URL.Query().Get("error_description"))
		s.failLogin(w, r, "the provider refused the login")
		return
	}

	cookie, err := r.Cookie(flowCookie)
	if err != nil {
		s.failLogin(w, r, "the login took too long, or did not start here")
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		s.failLogin(w, r, "the login state was unreadable")
		return
	}
	var flow auth.Flow
	if err := json.Unmarshal(raw, &flow); err != nil {
		s.failLogin(w, r, "the login state was unreadable")
		return
	}

	claims, err := s.gate.OIDC.Exchange(r.Context(), flow,
		r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		s.log.Warn("OIDC round trip failed", "error", err, "link", flow.Link)
		s.failLogin(w, r, "the login could not be completed")
		return
	}

	// A link round trip records who just proved themselves and stops there. It
	// re-checks the session rather than trusting the flow cookie: the cookie
	// only says a link was started, and starting one is not authority to
	// finish it.
	if flow.Link {
		current, ok := s.identify(r)
		if !ok || current.Subject != auth.LocalSubject {
			s.failLogin(w, r, "sign in as the built-in account before linking it")
			return
		}
		if err := s.gate.Account.Link(r.Context(), claims.Subject); err != nil {
			s.log.Error("link account", "error", err)
			s.failLogin(w, r, "the link could not be saved")
			return
		}
		s.log.Info("built-in account linked to a provider identity", "subject", claims.Display())
		http.Redirect(w, r, auth.SafeNext(flow.Next), http.StatusFound)
		return
	}

	// A linked identity is the built-in account, whatever the group rules say:
	// an explicit link is a more specific statement than a group membership.
	var id auth.Identity
	switch {
	case s.gate.Account.LinkedTo(claims.Subject):
		id = auth.Identity{Subject: auth.LocalSubject, Name: claims.Display(), Method: auth.MethodOIDC}
	case s.gate.OIDC.Allowed(claims):
		id = auth.Identity{Subject: claims.Subject, Name: claims.Display(), Method: auth.MethodOIDC}
	default:
		s.log.Warn("login refused", "subject", claims.Display(), "reason", "not in an allowed group")
		s.failLogin(w, r, "your account is not allowed to sign in to this Silt")
		return
	}

	token, err := s.gate.Sessions.Issue(r.Context(), id)
	if err != nil {
		s.log.Error("issue session", "error", err)
		s.failLogin(w, r, "could not start a session")
		return
	}
	setSessionCookie(w, r, token, s.gate.Sessions.TTL)
	s.log.Info("signed in", "method", "oidc", "subject", id.Name)
	s.audit(r, store.AuditSignIn, map[string]any{"method": "oidc", "subject": id.Name})
	http.Redirect(w, r, auth.SafeNext(flow.Next), http.StatusFound)
}

// failLogin sends the browser back to the app with a message it can render.
//
// A redirect rather than a JSON error, because the browser arrived here by
// navigation: showing raw JSON would be a dead end with no way back.
func (s *Server) failLogin(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/?login_error="+url.QueryEscape(message), http.StatusFound)
}

func clearFlowCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookie,
		Value:    "",
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   auth.SecureRequest(r),
		MaxAge:   -1,
	})
}

// sessionsResponse backs the "signed-in sessions" line on the settings screen.
type sessionsResponse struct {
	Count int64 `json:"count"`
}

func (s *Server) getSessions(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || s.gate.Sessions == nil {
		writeJSON(w, http.StatusOK, sessionsResponse{})
		return
	}
	count, err := s.gate.Sessions.Count(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count sessions")
		return
	}
	writeJSON(w, http.StatusOK, sessionsResponse{Count: count})
}

// deleteSessions signs every browser out, including this one.
//
// This is the button for "I think a token leaked". It is deliberately
// all-or-nothing rather than a list with individual revoke buttons: Silt has
// nothing to tell one session from another that would help someone choose.
func (s *Server) deleteSessions(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || s.gate.Sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "sessions are not in use")
		return
	}
	id, ok := s.identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	removed, err := s.gate.Sessions.RevokeSubject(r.Context(), id.Subject)
	if err != nil {
		s.log.Error("revoke sessions", "error", err)
		writeError(w, http.StatusInternalServerError, "could not revoke sessions")
		return
	}
	clearCookie(w, r, sessionCookie)
	s.log.Info("all sessions revoked", "subject", id.Subject, "count", removed)
	s.audit(r, store.AuditSessionsRevoked, map[string]any{"count": removed})
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": removed})
}

// --- the built-in account -------------------------------------------------

type passwordRequest struct {
	Current  string `json:"current,omitempty"`
	Password string `json:"password"`
}

func decodeJSON[T any](w http.ResponseWriter, r *http.Request, out *T) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return false
	}
	return true
}

// otherWayIn reports whether something besides the built-in account could
// admit someone.
func (g *Gate) otherWayIn() bool {
	return g != nil && (g.OIDC.Enabled() || g.Proxy.Enabled())
}

// postSetup claims the built-in account with its first password.
//
// Anonymous only when the account is the only way in. On a fresh install with
// nothing else configured that is the sole route, and the door is shut until
// it is used — the same first-run window every setup screen has, narrowed by
// refusing every other endpoint until it closes.
//
// The moment a provider or a proxy could let someone in, an anonymous claim
// would be an escalation rather than a bootstrap: a stranger would be taking
// an account that bypasses the provider. So it requires a session, which means
// signing in the way the install is already set up to do.
func (s *Server) postSetup(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.setupRequired() {
		writeError(w, http.StatusConflict, "this Silt has already been set up")
		return
	}
	if s.gate.otherWayIn() {
		if _, ok := s.identify(r); !ok {
			writeError(w, http.StatusUnauthorized,
				"sign in with your identity provider first, then set a password under Settings → Security")
			return
		}
	}

	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.gate.Account.Claim(r.Context(), req.Password); err != nil {
		var weak *auth.ErrWeakPassword
		if errors.As(err, &weak) {
			writeError(w, http.StatusBadRequest, weak.Reason)
			return
		}
		if errors.Is(err, auth.ErrNotClaimable) {
			writeError(w, http.StatusConflict, "this Silt has already been set up")
			return
		}
		s.log.Error("claim account", "error", err)
		writeError(w, http.StatusInternalServerError, "could not set the password")
		return
	}

	// Signed in immediately: making someone type the password they just chose
	// is ceremony, and they have already proved they know it.
	token, err := s.gate.Sessions.Issue(r.Context(), auth.Identity{
		Subject: auth.LocalSubject, Name: "admin", Method: auth.MethodPassword,
	})
	if err != nil {
		s.log.Error("issue session", "error", err)
		writeError(w, http.StatusInternalServerError, "the password was set but the session could not start")
		return
	}
	setSessionCookie(w, r, token, s.gate.Sessions.TTL)
	s.log.Info("built-in account claimed", "remote", clientKey(r))
	s.audit(r, store.AuditAccountClaimed, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

// putPassword changes the password of the signed-in built-in account.
func (s *Server) putPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := s.identify(r)
	if !ok || id.Subject != auth.LocalSubject {
		writeError(w, http.StatusForbidden, "only the built-in account can change its own password")
		return
	}

	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.gate.Account.ChangePassword(r.Context(), clientKey(r), req.Current, req.Password); err != nil {
		var weak *auth.ErrWeakPassword
		if errors.As(err, &weak) {
			writeError(w, http.StatusBadRequest, weak.Reason)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Every other session belonged to the old password. Changing it because
	// you think it leaked should not leave the copy that leaked still working.
	if removed, err := s.gate.Sessions.RevokeSubject(r.Context(), auth.LocalSubject); err != nil {
		s.log.Error("revoke sessions after password change", "error", err)
	} else {
		s.log.Info("password changed; sessions revoked", "count", removed)
	}
	token, err := s.gate.Sessions.Issue(r.Context(), id)
	if err != nil {
		s.log.Error("issue session", "error", err)
		writeError(w, http.StatusInternalServerError, "the password changed but the session could not restart")
		return
	}
	setSessionCookie(w, r, token, s.gate.Sessions.TTL)
	s.audit(r, store.AuditPasswordChanged, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"changed": true})
}

type accountStateRequest struct {
	Enabled bool `json:"enabled"`
}

// putAccount turns the built-in account on or off.
func (s *Server) putAccount(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.Account.Available() {
		writeError(w, http.StatusServiceUnavailable, "the built-in account is disabled by configuration")
		return
	}
	var req accountStateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Refusing to lock the owner out is the whole reason this is a handler
	// rather than a direct call: only this layer knows whether anything else
	// would still let someone in.
	if !req.Enabled && !s.gate.OIDC.Enabled() && !s.gate.Proxy.Enabled() {
		writeError(w, http.StatusConflict,
			"turning off the built-in account would leave no way to sign in; configure a provider or a reverse proxy first")
		return
	}
	if err := s.gate.Account.SetEnabled(r.Context(), req.Enabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.Enabled {
		if _, err := s.gate.Sessions.RevokeSubject(r.Context(), auth.LocalSubject); err != nil {
			s.log.Error("revoke sessions after disabling the account", "error", err)
		}
	}
	s.log.Info("built-in account state changed", "enabled", req.Enabled)
	s.audit(r, store.AuditAccountChanged, map[string]any{"password_enabled": req.Enabled})
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

// getLink starts an OpenID Connect round trip whose purpose is to record which
// provider identity belongs to the built-in account.
func (s *Server) getLink(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.OIDC.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "OpenID Connect is not configured")
		return
	}
	id, ok := s.identify(r)
	if !ok || id.Subject != auth.LocalSubject {
		writeError(w, http.StatusForbidden, "sign in as the built-in account to link it")
		return
	}

	url, flow, err := s.gate.OIDC.Start(r.URL.Query().Get("next"), true, requestBase(r))
	if err != nil {
		s.log.Error("start OIDC link", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start the link")
		return
	}
	if !s.writeFlowCookie(w, r, flow) {
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// deleteLink forgets the linked provider identity.
func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.Account.Available() {
		writeError(w, http.StatusServiceUnavailable, "the built-in account is disabled by configuration")
		return
	}
	id, ok := s.identify(r)
	if !ok || id.Subject != auth.LocalSubject {
		writeError(w, http.StatusForbidden, "sign in as the built-in account to unlink it")
		return
	}
	if err := s.gate.Account.Link(r.Context(), ""); err != nil {
		writeError(w, http.StatusInternalServerError, "could not unlink")
		return
	}
	s.log.Info("built-in account unlinked from its provider identity")
	s.audit(r, store.AuditAccountChanged, map[string]any{"provider_link": "removed"})
	writeJSON(w, http.StatusOK, map[string]bool{"linked": false})
}

// requestBase reconstructs the origin this request was addressed to, so the
// OpenID Connect callback can be derived when nothing was configured.
//
// Behind a reverse proxy the Host and scheme Silt sees are the proxy's own, so
// the forwarded headers are what carry the name a browser actually used. They
// are believed here because the only thing they decide is a URL sent to the
// provider — and the provider redirects only to URIs registered with it, so a
// forged Host produces a refusal rather than a redirect somewhere else.
func requestBase(r *http.Request) string {
	scheme := "http"
	if auth.SecureRequest(r) {
		scheme = "https"
	}
	host := firstValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// firstValue takes the client-most entry of a comma-separated forwarded header.
func firstValue(header string) string {
	if idx := strings.Index(header, ","); idx >= 0 {
		header = header[:idx]
	}
	return strings.TrimSpace(header)
}
