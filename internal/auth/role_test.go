package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unmaykr-a/silt/internal/auth"
)

// The reader/administrator split. Silt is already read-only against Docker;
// this is the split between reading the journal and changing Silt's own
// configuration. See PROJECT.md Section 14.

func TestNoAdminGroupsMeansEveryoneIsAnAdministrator(t *testing.T) {
	// What Silt did before roles existed. Turning an upgrade into a lockout
	// for the person who configured it would be the worst possible default.
	if got := auth.RoleFromGroups(nil, []string{"anything"}); got != auth.RoleAdmin {
		t.Errorf("role = %s, want admin", got)
	}
	if got := auth.RoleFromGroups(nil, nil); got != auth.RoleAdmin {
		t.Errorf("role with no groups at all = %s, want admin", got)
	}
}

func TestAdminGroupMembershipDecides(t *testing.T) {
	admins := []string{"silt-admins", "platform"}

	if got := auth.RoleFromGroups(admins, []string{"users", "platform"}); got != auth.RoleAdmin {
		t.Errorf("a member of an admin group = %s, want admin", got)
	}
	if got := auth.RoleFromGroups(admins, []string{"users"}); got != auth.RoleViewer {
		t.Errorf("a non-member = %s, want viewer", got)
	}
	if got := auth.RoleFromGroups(admins, nil); got != auth.RoleViewer {
		t.Errorf("someone with no groups = %s, want viewer", got)
	}
	// Providers disagree about case, and a lockout over capitalisation is a
	// support question nobody should have to ask.
	if got := auth.RoleFromGroups(admins, []string{"Silt-Admins"}); got != auth.RoleAdmin {
		t.Errorf("case-different group = %s, want admin", got)
	}
}

func TestParseRoleDefaultsToAdmin(t *testing.T) {
	// Every path reaching here predates roles or is the operator's own
	// account. A viewer is only ever produced by a rule that says so.
	for _, in := range []string{"", "admin", "nonsense", "Admin"} {
		if got := auth.ParseRole(in); got != auth.RoleAdmin {
			t.Errorf("ParseRole(%q) = %s, want admin", in, got)
		}
	}
	if got := auth.ParseRole("viewer"); got != auth.RoleViewer {
		t.Errorf("ParseRole(\"viewer\") = %s, want viewer", got)
	}
}

func TestIsAdmin(t *testing.T) {
	if !(auth.Identity{Role: auth.RoleAdmin}).IsAdmin() {
		t.Error("an admin identity is not admin")
	}
	if (auth.Identity{Role: auth.RoleViewer}).IsAdmin() {
		t.Error("a viewer identity is admin")
	}
	// The zero value is what every pre-roles code path produces.
	if !(auth.Identity{}).IsAdmin() {
		t.Error("the zero identity is not admin")
	}
}

func TestForwardAuthReadsGroupsOnlyWhenThereIsARule(t *testing.T) {
	request := func(user, groups string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		r.RemoteAddr = "10.0.0.9:1234"
		r.Header.Set("X-Remote-User", user)
		if groups != "" {
			r.Header.Set("X-Remote-Groups", groups)
		}
		return r
	}

	// With no admin groups configured, the header is not consulted at all and
	// everyone the proxy asserts is an administrator.
	plain, err := auth.NewProxy(true, "X-Remote-User", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	id, ok := plain.Identify(request("alice", "nobody"))
	if !ok || !id.IsAdmin() {
		t.Errorf("without admin groups: ok=%v role=%s, want ok and admin", ok, id.Role)
	}

	split, err := auth.NewProxy(true, "X-Remote-User", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	split = split.WithAdminGroups("X-Remote-Groups", []string{"silt-admins"})

	id, ok = split.Identify(request("alice", "users,silt-admins"))
	if !ok || !id.IsAdmin() {
		t.Errorf("a member: ok=%v role=%s, want ok and admin", ok, id.Role)
	}
	id, ok = split.Identify(request("bob", "users"))
	if !ok || id.IsAdmin() {
		t.Errorf("a non-member: ok=%v role=%s, want ok and viewer", ok, id.Role)
	}
	// No groups header at all is a viewer, not an administrator: a proxy that
	// forgot to send groups must not hand out administration.
	id, ok = split.Identify(request("carol", ""))
	if !ok || id.IsAdmin() {
		t.Errorf("no groups header: ok=%v role=%s, want ok and viewer", ok, id.Role)
	}
}

func TestForwardAuthGroupsAreOnlyBelievedFromATrustedPeer(t *testing.T) {
	// The groups header is exactly as trustworthy as the identity header, and
	// for the same reason: the trust list already decided this peer may assert
	// things. An untrusted peer gets no identity, so no role either.
	p, err := auth.NewProxy(true, "X-Remote-User", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	p = p.WithAdminGroups("X-Remote-Groups", []string{"silt-admins"})

	r := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	r.RemoteAddr = "192.168.1.5:9999"
	r.Header.Set("X-Remote-User", "mallory")
	r.Header.Set("X-Remote-Groups", "silt-admins")

	if _, ok := p.Identify(r); ok {
		t.Error("an untrusted peer was identified")
	}
}
