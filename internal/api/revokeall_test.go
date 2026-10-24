package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/unmaykr-a/silt/internal/auth"
)

// "Revoke all sessions" revoked one subject's.
//
// The screen above the button counted every session on the install and said
// the button revoked all of them; the handler called RevokeSubject with the
// caller's own subject. On a lone built-in account those are the same set,
// which is why it looked right. With a provider in play they are not, and the
// button exists precisely because a credential is loose -- usually somebody
// else's. The count left on screen afterwards was the proof nobody read.

func sessionCount(t *testing.T, f *accountFixture) int64 {
	t.Helper()
	resp, err := f.client.Get(f.srv.URL + "/api/auth/sessions")
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Count int64 `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode count: %v", err)
	}
	return out.Count
}

func TestRevokingSessionsEndsEveryoneElsesToo(t *testing.T) {
	f := newAccountFixture(t, "")
	if err := f.account.Claim(t.Context(), goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Somebody who signed in through a provider, which is the case the old
	// behaviour left running.
	other, err := f.gate.Sessions.Issue(t.Context(), auth.Identity{
		Subject: "oidc|alice", Name: "alice",
		Method: auth.MethodOIDC, Role: auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	loginTo(t, f) // the caller's own session, through the built-in account

	if got := sessionCount(t, f); got != 2 {
		t.Fatalf("sessions before = %d, want 2", got)
	}

	req, err := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/auth/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d", resp.StatusCode)
	}
	var body struct {
		Revoked int64 `json:"revoked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Revoked != 2 {
		t.Errorf("revoked = %d, want 2: the screen counts every session and the button says it revokes all of them", body.Revoked)
	}

	// The one that matters: the other identity's token is dead, not merely
	// uncounted.
	if _, err := f.gate.Sessions.Lookup(t.Context(), other); err == nil {
		t.Error("another identity's session still works after revoking all sessions")
	}
	if got := sessionCount(t, f); got != 0 {
		t.Errorf("sessions after = %d, want 0", got)
	}
}
