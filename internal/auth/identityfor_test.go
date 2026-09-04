package auth_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/store"
)

// Turning verified claims into an identity, which had two bugs in it.
//
// The role was never read: the callback built the Identity by hand and left
// Role unset, and an unset role parses as administrator. So
// SILT_OIDC_ADMIN_GROUPS was configured, shown on the settings screen, and did
// nothing — every account the provider admitted was an administrator. Forward
// auth was fine, because it re-reads its groups from the header on every
// request and never went through this path.
//
// And a linked subject skipped the allowlist, because the link was checked
// first and on its own.

func account(t *testing.T) *auth.Account {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := auth.LoadAccount(context.Background(), db, "", true)
	if err != nil {
		t.Fatalf("LoadAccount: %v", err)
	}
	return a
}

func TestTheRoleComesFromTheProvidersGroups(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, func(c *auth.OIDCConfig) {
		c.AdminGroups = []string{"silt-admins"}
	})
	acct := account(t)

	admin, ok := auth.IdentityFor(o, acct, auth.Claims{
		Subject: "alice", Username: "alice", Groups: []string{"users", "silt-admins"},
	})
	if !ok {
		t.Fatal("a member of the admin group was refused")
	}
	if !admin.IsAdmin() {
		t.Errorf("a member of the admin group is %s, want admin", admin.Role)
	}

	viewer, ok := auth.IdentityFor(o, acct, auth.Claims{
		Subject: "bob", Username: "bob", Groups: []string{"users"},
	})
	if !ok {
		t.Fatal("someone outside the admin group was refused entirely; they should read")
	}
	// The bug: this used to come back as an administrator, because the role was
	// never set and an unset role parses as admin.
	if viewer.IsAdmin() {
		t.Errorf("someone outside the admin group is %s, want viewer", viewer.Role)
	}
}

func TestNoAdminGroupStillMeansEveryoneAdmits(t *testing.T) {
	// Unset has to keep meaning "everyone admitted may change everything", or
	// upgrading would demote the person who configured Silt.
	p := newFakeProvider(t)
	o := newOIDC(t, p, nil)
	id, ok := auth.IdentityFor(o, account(t), auth.Claims{Subject: "alice", Groups: []string{"users"}})
	if !ok || !id.IsAdmin() {
		t.Errorf("id = %+v, ok = %v; want an administrator", id, ok)
	}
}

func TestTheAllowlistIsAppliedBeforeTheLink(t *testing.T) {
	p := newFakeProvider(t)
	o := newOIDC(t, p, func(c *auth.OIDCConfig) {
		c.AllowedGroups = []string{"silt-users"}
	})
	acct := account(t)
	ctx := context.Background()
	if err := acct.Claim(ctx, "correct horse battery"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := acct.Link(ctx, "alice"); err != nil {
		t.Fatalf("link: %v", err)
	}

	// In the allowed group: the link decides which account, so this is the
	// built-in one.
	id, ok := auth.IdentityFor(o, acct, auth.Claims{Subject: "alice", Groups: []string{"silt-users"}})
	if !ok {
		t.Fatal("a linked, allowed identity was refused")
	}
	if id.Subject != auth.LocalSubject || !id.IsAdmin() {
		t.Errorf("id = %+v, want the built-in account as an administrator", id)
	}

	// Removed from the allowed group. The bug: the link was checked first, so
	// this still signed in — as the administrator.
	if id, ok := auth.IdentityFor(o, acct, auth.Claims{Subject: "alice", Groups: []string{"ex-staff"}}); ok {
		t.Errorf("a linked identity outside the allowlist signed in as %+v", id)
	}
}

func TestADisabledProviderAdmitsNobody(t *testing.T) {
	// Nothing reaches this today, because the callback is only routed when
	// OIDC is on. Fail closed anyway: the rest of the type is nil-safe, and
	// the safe answer to "is this person allowed" is no.
	if id, ok := auth.IdentityFor(nil, account(t), auth.Claims{Subject: "alice"}); ok {
		t.Errorf("a nil provider admitted %+v", id)
	}
}
