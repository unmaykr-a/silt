// Package auth is Silt's authentication: who is asking, and may they.
//
// Three ways in, in the order PROJECT.md Section 13 specifies. A forward-auth
// header from a reverse proxy, an OpenID Connect provider, and a bcrypt
// password as the fallback for someone with neither. With none of them
// configured Silt is open, which is the right default for a tool people put
// behind their own proxy — but it is said out loud at startup rather than left
// to be assumed.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// Method is how an identity was established.
type Method string

const (
	MethodPassword Method = "password"
	MethodOIDC     Method = "oidc"
	MethodProxy    Method = "proxy"
)

// Role is what an identity may do.
//
// Silt is already read-only against Docker; the split that matters is between
// reading the journal and changing Silt's own configuration. There is no roles
// table: the provider already manages groups, and duplicating them here would
// be two sources of truth that agree until they do not. See PROJECT.md
// Section 14.
type Role string

const (
	// RoleAdmin may change Silt's configuration.
	RoleAdmin Role = "admin"
	// RoleViewer may read every screen and change nothing but their own
	// appearance preferences, which live in their browser anyway.
	RoleViewer Role = "viewer"
)

// ParseRole reads a stored or configured role, defaulting to admin.
//
// Defaulting to admin rather than viewer on purpose: every path that reaches
// here predates roles or is the operator's own account, and silently demoting
// someone who could previously change everything looks like Silt breaking. A
// viewer is only ever produced by a rule that explicitly says so.
func ParseRole(v string) Role {
	if Role(v) == RoleViewer {
		return RoleViewer
	}
	return RoleAdmin
}

// Identity is who is asking, and what they may do.
type Identity struct {
	Subject string
	Name    string
	Method  Method
	Role    Role
	// AdminLapsed marks a session that was issued as an administrator and has
	// been one for longer than the provider's answer is good for.
	//
	// Carried rather than inferred so the refusal can say why. "Read-only
	// access" to someone who signed in as an administrator this morning is a
	// bug report; "your administrator rights expired, sign in again" is an
	// instruction.
	AdminLapsed bool
}

// IsAdmin reports whether this identity may change Silt's configuration.
func (i Identity) IsAdmin() bool { return i.Role != RoleViewer }

// LocalSubject is the subject recorded for the password login. There is one
// password, so there is one account behind it.
const LocalSubject = "local"

// Sessions issues and validates browser sessions.
//
// The token is opaque and random; what it means is read from the database
// rather than carried in the cookie, so there is nothing in it to forge and
// nothing to sign. Only its digest is stored, so a copy of the database is not
// a set of working sessions.
type Sessions struct {
	db *store.Store
	// TTL is the absolute lifetime of a session, however active it is.
	TTL time.Duration
	// IdleTTL ends a session that has not been used. Zero disables it.
	IdleTTL time.Duration
	// AdminTTL bounds how long a provider-granted administrator role survives
	// without a fresh sign-in. Zero disables the lapse. See config.Config.
	AdminTTL time.Duration
	// Now is swappable for tests.
	Now func() time.Time
}

// NewSessions returns a store-backed session issuer.
func NewSessions(db *store.Store, ttl, idle time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &Sessions{db: db, TTL: ttl, IdleTTL: idle, Now: time.Now}
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// hashToken is what turns the cookie value into the primary key. SHA-256
// rather than bcrypt deliberately: the token is 256 bits of entropy from
// crypto/rand, so there is no dictionary to slow an attacker down with, and
// a fast hash keeps the per-request lookup cheap.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Issue creates a session and returns the token the browser should hold.
func (s *Sessions) Issue(ctx context.Context, id Identity) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()

	if err := s.db.Q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		TokenHash:  hashToken(token),
		Subject:    id.Subject,
		Name:       id.Name,
		Method:     string(id.Method),
		Role:       string(ParseRole(string(id.Role))),
		CreatedAt:  now.UnixMilli(),
		LastSeenAt: now.UnixMilli(),
		ExpiresAt:  now.Add(s.TTL).UnixMilli(),
	}); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}
	return token, nil
}

// ErrNoSession means the token is unknown, expired, or idle out.
var ErrNoSession = errors.New("no valid session")

// Lookup validates a token and refreshes its last-seen time.
//
// An expired row is deleted rather than left to the retention sweep: the
// request that found it is the cheapest place to clean it up, and leaving it
// would mean a token that failed once could be presented again.
func (s *Sessions) Lookup(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrNoSession
	}
	hash := hashToken(token)
	row, err := s.db.RQ.GetSession(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNoSession
	}
	if err != nil {
		return Identity{}, fmt.Errorf("read session: %w", err)
	}

	now := s.now()
	expired := now.UnixMilli() >= row.ExpiresAt
	idle := s.IdleTTL > 0 && now.Sub(time.UnixMilli(row.LastSeenAt)) > s.IdleTTL
	if expired || idle {
		_ = s.db.Q.DeleteSession(ctx, hash)
		return Identity{}, ErrNoSession
	}

	// Written at most once a minute. Every request would be a write per
	// request on a database with a single writer, which is a lot of contention
	// to buy a more precise idle timeout than anyone needs.
	if now.Sub(time.UnixMilli(row.LastSeenAt)) > time.Minute {
		_ = s.db.Q.TouchSession(ctx, sqlcgen.TouchSessionParams{
			LastSeenAt: now.UnixMilli(),
			TokenHash:  hash,
		})
	}

	id := Identity{
		Subject: row.Subject,
		Name:    row.Name,
		Method:  Method(row.Method),
		Role:    ParseRole(row.Role),
	}
	if s.adminLapsed(id, time.UnixMilli(row.CreatedAt), now) {
		id.Role = RoleViewer
		id.AdminLapsed = true
	}
	return id, nil
}

// adminLapsed reports whether this session's administrator rights have outlived
// the answer that granted them.
//
// Only OIDC, and only admin. Forward auth re-reads its groups from the header
// on every request, so it is never stale. The built-in account has no external
// source that could have changed its mind, and expiring its rights would lock
// the sole operator out of their own settings on a timer. A viewer has nothing
// to lose.
func (s *Sessions) adminLapsed(id Identity, issued, now time.Time) bool {
	if s.AdminTTL <= 0 || id.Method != MethodOIDC || id.Role != RoleAdmin {
		return false
	}
	return now.Sub(issued) > s.AdminTTL
}

// Revoke ends one session. Unknown tokens are not an error: signing out twice
// is not a failure.
func (s *Sessions) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.db.Q.DeleteSession(ctx, hashToken(token))
}

// RevokeSubject ends every session belonging to one identity, which is what a
// changed password or a removed group membership should do.
func (s *Sessions) RevokeSubject(ctx context.Context, subject string) (int64, error) {
	return s.db.Q.DeleteSessionsForSubject(ctx, subject)
}

// Sweep removes expired and idle sessions. Called from the retention pass.
func (s *Sessions) Sweep(ctx context.Context) (int64, error) {
	now := s.now()
	idleBefore := int64(0)
	if s.IdleTTL > 0 {
		idleBefore = now.Add(-s.IdleTTL).UnixMilli()
	}
	return s.db.Q.DeleteExpiredSessions(ctx, sqlcgen.DeleteExpiredSessionsParams{
		ExpiresAt:  now.UnixMilli(),
		LastSeenAt: idleBefore,
	})
}

// Count reports how many sessions exist, for the settings screen.
func (s *Sessions) Count(ctx context.Context) (int64, error) {
	return s.db.RQ.CountSessions(ctx)
}
