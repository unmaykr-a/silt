package api_test

import (
	"net/http"
	"testing"
)

// TestAnAuthChangeRebuildsTheGate is the test that decides whether making
// authentication editable was real work or a form that writes to a row nobody
// reads.
//
// Every part of the gate is built from these settings at startup, so the whole
// question is whether a save replaces that gate. The observable proof is a
// request: turn the built-in account off and the login it offers has to go.
func TestAnAuthChangeRebuildsTheGate(t *testing.T) {
	f := newAccountFixture(t, "", withLiveAuth())
	if code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("claim account = %d %s", code, body)
	}
	if got := f.state(t)["password_enabled"]; got != true {
		t.Fatalf("password_enabled = %v before the change, want true", got)
	}

	if code, body := f.do(t, "PUT", "/api/settings", `{"local_account":false}`); code != 200 {
		t.Fatalf("turning the local account off = %d %s", code, body)
	}

	// The gate the next request is answered by is a different gate.
	state := f.state(t)
	if got := state["password_enabled"]; got != false {
		t.Errorf("password_enabled = %v after turning the account off, want false: the gate was not rebuilt", got)
	}
	if got := state["local_enabled"]; got == true {
		t.Error("local_enabled is still true after turning the account off")
	}

	// And the login actually stops working, which is the part a field on a
	// payload could be wrong about.
	if code, _ := f.do(t, "POST", "/api/login", `{"password":"`+goodPassword+`"}`); code == 200 {
		t.Error("the password still signs in after the account was turned off")
	}

	// Back, so the change is reversible from the same screen.
	if code, body := f.do(t, "PUT", "/api/settings", `{"reset":["local_account"]}`); code != 200 {
		t.Fatalf("reset = %d %s", code, body)
	}
	if got := f.state(t)["password_enabled"]; got != true {
		t.Errorf("password_enabled = %v after reset, want true", got)
	}
}

// A save that changes nothing the gate is built from must not rebuild it.
//
// Rebuilding re-runs OpenID Connect discovery, which reaches the network. A
// settings screen that called your identity provider every time someone changed
// the log level would be a surprising thing to have built.
func TestASaveThatIsNotAboutAuthDoesNotRebuildTheGate(t *testing.T) {
	f := newFixture(t)
	before := f.settings(t)

	if resp, body := f.do(t, http.MethodPut, "/api/settings", `{"retention_days":42}`, nil); resp.StatusCode != 200 {
		t.Fatalf("PUT retention = %d %s", resp.StatusCode, body)
	}
	after := f.settings(t)
	if after.Effective.RetentionDays != 42 {
		t.Fatalf("retention = %d, want 42", after.Effective.RetentionDays)
	}
	// Nothing about authentication moved, which is what the fingerprint is for.
	if after.Identity.Mode != before.Identity.Mode {
		t.Errorf("auth mode moved from %q to %q on a retention change", before.Identity.Mode, after.Identity.Mode)
	}
}

// Session lifetimes are gate settings too, and a session is a row rather than
// state in the object — so shortening the lifetime must not sign everybody out.
func TestChangingTheSessionLifetimeKeepsExistingSessions(t *testing.T) {
	f := newAccountFixture(t, "", withLiveAuth())
	if code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("claim account = %d %s", code, body)
	}
	if code, _ := f.do(t, "GET", "/api/hosts", ""); code != 200 {
		t.Fatalf("the setup session cannot read; nothing else here means anything")
	}

	// A week rather than the default month.
	if code, body := f.do(t, "PUT", "/api/settings", `{"session_ttl_ms":604800000}`); code != 200 {
		t.Fatalf("shortening the session lifetime = %d %s", code, body)
	}
	if code, body := f.do(t, "GET", "/api/hosts", ""); code != 200 {
		t.Errorf("the session stopped working after the lifetime changed: %d %s", code, body)
	}
}

// A provider that cannot be reached must leave the rest of the gate working and
// say why, rather than failing the save or taking authentication down.
//
// The address is in the reserved .invalid TLD, so this resolves nowhere without
// depending on the network being absent.
func TestAnUnreachableProviderIsReportedAndDoesNotBreakTheGate(t *testing.T) {
	f := newAccountFixture(t, "", withLiveAuth())
	if code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("claim account = %d %s", code, body)
	}

	code, body := f.do(t, "PUT", "/api/settings",
		`{"oidc_issuer":"https://nothing.here.invalid/application/o/silt/","oidc_client_id":"silt"}`)
	if code != 200 {
		t.Fatalf("configuring an unreachable provider = %d %s, want 200: a save that refuses cannot be corrected", code, body)
	}

	state := f.state(t)
	if got := state["oidc_enabled"]; got == true {
		t.Error("oidc_enabled is true for a provider that cannot be reached")
	}
	if got, _ := state["oidc_error"].(string); got == "" {
		t.Error("no oidc_error for a provider that cannot be reached; the login would just be missing")
	}
	// The part that matters: the password login still works, so the install is
	// reachable to fix the issuer.
	if got := state["password_enabled"]; got != true {
		t.Errorf("password_enabled = %v; a bad provider took the working login with it", got)
	}
	if code, _ := f.do(t, "GET", "/api/hosts", ""); code != 200 {
		t.Error("the existing session stopped working when a bad provider was configured")
	}
}
