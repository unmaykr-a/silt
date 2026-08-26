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
	Password *auth.Password
	Proxy    *auth.Proxy
	OIDC     *auth.OIDC
	// MetricsPublic leaves /metrics reachable without authentication.
	MetricsPublic bool
	// AllowedOrigins are extra origins accepted on unsafe requests, beyond the
	// one the request was addressed to.
	AllowedOrigins []string
}

// Enabled reports whether any authentication is configured. With none, Silt is
// open — the right default for something behind someone's own proxy, and
// warned about at startup rather than left to be assumed.
func (g *Gate) Enabled() bool {
	if g == nil {
		return false
	}
	return g.Proxy.Enabled() || g.Password.Enabled() || g.OIDC.Enabled()
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
		"/api/login", "/api/logout", "/api/auth",
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
	Authenticated   bool   `json:"authenticated"`
	Subject         string `json:"subject,omitempty"`
	Method          string `json:"method,omitempty"`
}

func (s *Server) getAuthState(w http.ResponseWriter, r *http.Request) {
	out := authStateResponse{
		Required:        s.gate.Enabled(),
		PasswordEnabled: s.gate != nil && s.gate.Password.Enabled(),
		OIDCEnabled:     s.gate != nil && s.gate.OIDC.Enabled(),
		ProxyEnabled:    s.gate != nil && s.gate.Proxy.Enabled(),
	}
	if s.gate != nil {
		out.OIDCIssuer = s.gate.OIDC.Issuer()
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
	if s.gate == nil || !s.gate.Password.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "password login is not configured")
		return
	}

	client := clientKey(r)
	if blocked, wait := s.gate.Password.Throttled(client); blocked {
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
	if !s.gate.Password.Verify(client, req.Password) {
		s.log.Warn("failed login attempt", "remote", client)
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
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// getOIDCLogin starts the OpenID Connect flow.
func (s *Server) getOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.gate == nil || !s.gate.OIDC.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "OpenID Connect is not configured")
		return
	}

	url, flow, err := s.gate.OIDC.Start(r.URL.Query().Get("next"))
	if err != nil {
		s.log.Error("start OIDC login", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start the login")
		return
	}

	encoded, err := json.Marshal(flow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the login")
		return
	}
	// The flow state rides in a cookie rather than server state: there is
	// exactly one consumer and it arrives with the callback, so a table would
	// buy nothing but rows to expire. SameSite=Lax is required, not incidental
	// — the callback is a top-level cross-site navigation from the provider,
	// and Strict would drop the cookie on arrival.
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookie,
		Value:    base64.RawURLEncoding.EncodeToString(encoded),
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   auth.SecureRequest(r),
		MaxAge:   int(flowMaxAge.Seconds()),
	})
	http.Redirect(w, r, url, http.StatusFound)
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

	id, err := s.gate.OIDC.Finish(r.Context(), flow,
		r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		var refused *auth.ErrNotAllowed
		if errors.As(err, &refused) {
			s.log.Warn("login refused", "subject", refused.Subject, "reason", "not in an allowed group")
			s.failLogin(w, r, "your account is not allowed to sign in to this Silt")
			return
		}
		s.log.Warn("OIDC login failed", "error", err)
		s.failLogin(w, r, "the login could not be completed")
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
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": removed})
}
