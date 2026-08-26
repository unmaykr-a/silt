package auth_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/store"
)

func sessions(t *testing.T, ttl, idle time.Duration) (*auth.Sessions, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "silt.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return auth.NewSessions(db, ttl, idle), path
}

func TestSessionRoundTrip(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	ctx := t.Context()

	token, err := s.Issue(ctx, auth.Identity{Subject: "sub-1", Name: "andri", Method: auth.MethodOIDC})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	id, err := s.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if id.Subject != "sub-1" || id.Name != "andri" || id.Method != auth.MethodOIDC {
		t.Errorf("identity = %+v, want the one that was issued", id)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	for _, token := range []string{"", "nonsense", strings.Repeat("A", 43)} {
		if _, err := s.Lookup(t.Context(), token); !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("Lookup(%q) error = %v, want ErrNoSession", token, err)
		}
	}
}

// The token is what the browser holds; the database must not be a set of
// working sessions if it is copied.
func TestTokenIsNotStoredInTheDatabase(t *testing.T) {
	s, path := sessions(t, time.Hour, 0)
	token, err := s.Issue(t.Context(), auth.Identity{Subject: "sub-1", Method: auth.MethodPassword})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Read the file itself rather than querying: this is about what is on disk.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read database: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Error("the session token appears verbatim in the database file")
	}
}

func TestExpiredSessionIsRejectedAndRemoved(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	now := time.Now()
	s.Now = func() time.Time { return now }

	token, err := s.Issue(t.Context(), auth.Identity{Subject: "sub-1", Method: auth.MethodPassword})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := s.Lookup(t.Context(), token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("expired lookup error = %v, want ErrNoSession", err)
	}
	// Cleaned up by the request that found it, so a token that failed once
	// cannot be presented again against a lingering row.
	count, err := s.Count(t.Context())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("expired session left %d rows behind", count)
	}
}

func TestIdleSessionExpires(t *testing.T) {
	s, _ := sessions(t, 30*24*time.Hour, time.Hour)
	now := time.Now()
	s.Now = func() time.Time { return now }

	token, _ := s.Issue(t.Context(), auth.Identity{Subject: "sub-1", Method: auth.MethodPassword})

	// Used within the window: still valid, and the clock restarts.
	now = now.Add(50 * time.Minute)
	if _, err := s.Lookup(t.Context(), token); err != nil {
		t.Fatalf("within the idle window: %v", err)
	}
	now = now.Add(50 * time.Minute)
	if _, err := s.Lookup(t.Context(), token); err != nil {
		t.Fatalf("the idle clock did not restart on use: %v", err)
	}
	// Left alone past the window: gone, even though the absolute TTL is weeks.
	now = now.Add(2 * time.Hour)
	if _, err := s.Lookup(t.Context(), token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("idle lookup error = %v, want ErrNoSession", err)
	}
}

// Signing out must revoke server-side. A cookie the browser throws away is
// still a working credential to anyone who copied it.
func TestRevokeEndsTheSession(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	ctx := t.Context()
	token, _ := s.Issue(ctx, auth.Identity{Subject: "sub-1", Method: auth.MethodPassword})

	if err := s.Revoke(ctx, token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Lookup(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("a revoked token still works: %v", err)
	}
	// Signing out twice is not a failure.
	if err := s.Revoke(ctx, token); err != nil {
		t.Errorf("revoking twice: %v", err)
	}
}

func TestRevokeSubjectEndsEverySessionForOnePerson(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	ctx := t.Context()

	var mine []string
	for i := 0; i < 3; i++ {
		token, _ := s.Issue(ctx, auth.Identity{Subject: "sub-1", Method: auth.MethodOIDC})
		mine = append(mine, token)
	}
	other, _ := s.Issue(ctx, auth.Identity{Subject: "sub-2", Method: auth.MethodOIDC})

	removed, err := s.RevokeSubject(ctx, "sub-1")
	if err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	if removed != 3 {
		t.Errorf("revoked %d, want 3", removed)
	}
	for _, token := range mine {
		if _, err := s.Lookup(ctx, token); !errors.Is(err, auth.ErrNoSession) {
			t.Error("a revoked session survived")
		}
	}
	if _, err := s.Lookup(ctx, other); err != nil {
		t.Errorf("another person's session was revoked too: %v", err)
	}
}

func TestSweepRemovesExpiredSessions(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	ctx := t.Context()
	now := time.Now()
	s.Now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, err := s.Issue(ctx, auth.Identity{Subject: "sub-1", Method: auth.MethodPassword}); err != nil {
			t.Fatalf("Issue: %v", err)
		}
	}
	now = now.Add(2 * time.Hour)
	removed, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 3 {
		t.Errorf("swept %d, want 3", removed)
	}
}

// Two sessions must never collide, however many are issued.
func TestTokensAreDistinct(t *testing.T) {
	s, _ := sessions(t, time.Hour, 0)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		token, err := s.Issue(t.Context(), auth.Identity{Subject: "sub-1", Method: auth.MethodPassword})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if seen[token] {
			t.Fatal("two sessions were issued the same token")
		}
		seen[token] = true
		if len(token) < 40 {
			t.Fatalf("token %q is shorter than 256 bits of entropy would produce", token)
		}
	}
}
