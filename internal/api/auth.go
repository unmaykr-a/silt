package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Authentication, in the order the brief specifies: a forward-auth header from
// a reverse proxy first, with a password as the fallback for people without an
// identity provider. See PROJECT.md Section 13.
//
// With neither configured, Silt is open. That is the right default for a tool
// people put behind their own proxy, but it is worth saying out loud at
// startup rather than leaving someone to assume otherwise.

const (
	sessionCookie = "silt_session"
	sessionMaxAge = 30 * 24 * time.Hour
)

// Auth holds the configured authentication.
type Auth struct {
	trustProxy   bool
	headerName   string
	passwordHash string
	// signingKey authenticates session cookies. Regenerated on restart, which
	// logs everyone out — acceptable for a single-user tool, and it means no
	// long-lived secret to store.
	signingKey []byte
}

// NewAuth builds an Auth. It returns an error if the password hash is not a
// valid bcrypt hash, since a typo there would silently lock the owner out.
func NewAuth(trustProxy bool, headerName, passwordHash string) (*Auth, error) {
	a := &Auth{
		trustProxy:   trustProxy,
		headerName:   headerName,
		passwordHash: strings.TrimSpace(passwordHash),
		signingKey:   make([]byte, 32),
	}
	if a.headerName == "" {
		a.headerName = "X-Remote-User"
	}
	if a.passwordHash != "" {
		if _, err := bcrypt.Cost([]byte(a.passwordHash)); err != nil {
			return nil, fmt.Errorf("SILT_PASSWORD_HASH is not a valid bcrypt hash: %w", err)
		}
	}
	if _, err := rand.Read(a.signingKey); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	return a, nil
}

// Enabled reports whether any authentication is configured.
func (a *Auth) Enabled() bool {
	return a != nil && (a.trustProxy || a.passwordHash != "")
}

// PasswordEnabled reports whether the password fallback is available.
func (a *Auth) PasswordEnabled() bool { return a != nil && a.passwordHash != "" }

// authorised reports whether a request carries a valid identity.
func (a *Auth) authorised(r *http.Request) bool {
	if !a.Enabled() {
		return true
	}
	if a.trustProxy && strings.TrimSpace(r.Header.Get(a.headerName)) != "" {
		return true
	}
	if a.passwordHash == "" {
		return false
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return a.validSession(cookie.Value)
}

// issueSession returns a signed, expiring session value. The payload is only
// an expiry: there is one account, so there is no identity to carry.
func (a *Auth) issueSession() string {
	expiry := strconv.FormatInt(time.Now().Add(sessionMaxAge).Unix(), 10)
	mac := hmac.New(sha256.New, a.signingKey)
	mac.Write([]byte(expiry))
	return expiry + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) validSession(value string) bool {
	expiry, signature, found := strings.Cut(value, ".")
	if !found {
		return false
	}
	mac := hmac.New(sha256.New, a.signingKey)
	mac.Write([]byte(expiry))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(want)) {
		return false
	}
	ts, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < ts
}

// isPublic reports whether a path is reachable without authentication.
//
// Health probes must answer for orchestrators that cannot present credentials,
// /metrics for a Prometheus scrape on a private network, and /api/ingest
// carries its own token. Login has to be reachable to log in at all.
func isPublic(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics", "/api/login", "/api/auth":
		return true
	case "/api/ingest":
		return true
	default:
		return false
	}
}

// requireAuth wraps the route tree.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.Enabled() || isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.auth.authorised(r) {
			next.ServeHTTP(w, r)
			return
		}
		// The SPA needs to load in order to show a login form, so an
		// unauthenticated browser navigation gets the app; only API calls are
		// refused outright.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authStateResponse tells the UI what it is dealing with.
type authStateResponse struct {
	Required        bool `json:"required"`
	PasswordEnabled bool `json:"password_enabled"`
	Authenticated   bool `json:"authenticated"`
}

func (s *Server) getAuthState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authStateResponse{
		Required:        s.auth.Enabled(),
		PasswordEnabled: s.auth.PasswordEnabled(),
		Authenticated:   s.auth.authorised(r),
	})
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.PasswordEnabled() {
		writeError(w, http.StatusServiceUnavailable, "password login is not configured")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	// bcrypt is deliberately slow, which is the point, and it compares in
	// constant time for us.
	if bcrypt.CompareHashAndPassword([]byte(s.auth.passwordHash), []byte(req.Password)) != nil {
		s.log.Warn("failed login attempt", "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "incorrect password")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.auth.issueSession(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure is not set: Silt is commonly reached over plain HTTP on a
		// LAN, and a cookie that never arrives is worse than one that relies
		// on the operator's own network boundary. Terminate TLS at the proxy.
		MaxAge: int(sessionMaxAge.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Server) postLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}
