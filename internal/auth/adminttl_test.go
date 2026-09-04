package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/auth"
)

// A provider's groups are read once, at sign-in, and nothing re-reads them.
// With a 720h session that made "remove them from the admin group" a change
// that took effect up to a month later — which is not what anyone doing it
// believes. Only the administrator half lapses, and only where the answer came
// from a provider.

func sessionsWithAdminTTL(t *testing.T, ttl time.Duration) (*auth.Sessions, func(time.Duration)) {
	t.Helper()
	s, _ := sessions(t, 720*time.Hour, 0)
	s.AdminTTL = ttl
	now := time.Now()
	s.Now = func() time.Time { return now }
	return s, func(d time.Duration) { now = now.Add(d) }
}

func TestAProviderAdminLapsesToViewer(t *testing.T) {
	s, advance := sessionsWithAdminTTL(t, 12*time.Hour)
	ctx := context.Background()

	token, err := s.Issue(ctx, auth.Identity{
		Subject: "alice", Name: "Alice", Method: auth.MethodOIDC, Role: auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	id, err := s.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !id.IsAdmin() || id.AdminLapsed {
		t.Fatalf("a fresh session is not an administrator: %+v", id)
	}

	advance(11 * time.Hour)
	if id, _ = s.Lookup(ctx, token); !id.IsAdmin() {
		t.Error("the role lapsed inside its window")
	}

	advance(2 * time.Hour)
	id, err = s.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("the session itself ended, it should only have been demoted: %v", err)
	}
	if id.IsAdmin() {
		t.Error("the administrator role outlived its window")
	}
	if !id.AdminLapsed {
		t.Error("the lapse is not reported, so the refusal cannot say why")
	}
	// Reading is not the dangerous part, so it keeps working.
	if id.Subject != "alice" || id.Role != auth.RoleViewer {
		t.Errorf("identity after the lapse = %+v, want alice as a viewer", id)
	}
}

func TestTheBuiltInAccountDoesNotLapse(t *testing.T) {
	// It has no provider to have changed its mind, and expiring its rights
	// would lock the sole operator out of their own settings on a timer.
	s, advance := sessionsWithAdminTTL(t, time.Hour)
	ctx := context.Background()
	token, err := s.Issue(ctx, auth.Identity{
		Subject: auth.LocalSubject, Method: auth.MethodPassword, Role: auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	advance(72 * time.Hour)
	id, err := s.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !id.IsAdmin() {
		t.Error("the built-in account lost its own settings screen to a timer")
	}
}

func TestAViewerHasNothingToLapse(t *testing.T) {
	s, advance := sessionsWithAdminTTL(t, time.Hour)
	ctx := context.Background()
	token, err := s.Issue(ctx, auth.Identity{
		Subject: "bob", Method: auth.MethodOIDC, Role: auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	advance(72 * time.Hour)
	id, err := s.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if id.AdminLapsed {
		t.Error("a viewer was reported as having lapsed from something")
	}
	if id.Role != auth.RoleViewer {
		t.Errorf("role = %s", id.Role)
	}
}

func TestZeroDisablesTheLapse(t *testing.T) {
	s, advance := sessionsWithAdminTTL(t, 0)
	ctx := context.Background()
	token, err := s.Issue(ctx, auth.Identity{
		Subject: "alice", Method: auth.MethodOIDC, Role: auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Well past any admin window, and still inside the session's own 720h.
	advance(20 * 24 * time.Hour)
	id, err := s.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !id.IsAdmin() {
		t.Error("the role lapsed with the lapse turned off")
	}
}
