package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/config"
)

// The Secure flag used to be inferred from the request and nothing else, which
// is right until a reverse proxy terminates TLS and forgets
// X-Forwarded-Proto — then Silt looks at a plain HTTP request and ships the
// session cookie without Secure, over a connection a browser will happily
// repeat in the clear. Failing open in the one direction that matters.

func sessionCookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "silt_session" {
			return c
		}
	}
	t.Fatal("no session cookie was set")
	return nil
}

func loginTo(t *testing.T, f *accountFixture) *http.Response {
	t.Helper()
	resp, err := f.client.Post(f.srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"password":"`+goodPassword+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return resp
}

func TestTheCookieCanBeToldToAlwaysBeSecure(t *testing.T) {
	// The httptest server is plain HTTP and sets no forwarded header, so this
	// is exactly the shape "auto" gets wrong.
	f := newAccountFixture(t, "", withCookieSecure(config.CookieSecureAlways))
	if err := f.account.Claim(t.Context(), goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if c := sessionCookieFrom(t, loginTo(t, f)); !c.Secure {
		t.Error("SILT_COOKIE_SECURE=always did not set Secure")
	}
}

func TestAutoStillInfersItFromThePlainRequest(t *testing.T) {
	f := newAccountFixture(t, "", withCookieSecure(config.CookieSecureAuto))
	if err := f.account.Claim(t.Context(), goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Plain HTTP with no forwarded header: a Secure cookie would never come
	// back, which is worse than relying on the operator's own network.
	if c := sessionCookieFrom(t, loginTo(t, f)); c.Secure {
		t.Error("auto set Secure on a plain HTTP request")
	}
}

func TestAnHTTPSBaseURLCountsAsKnowing(t *testing.T) {
	// Someone who has told Silt its public address is https has already
	// answered this question; making them answer it twice is a trap.
	f := newAccountFixture(t, "", withBaseURL("https://silt.example.lan"))
	if err := f.account.Claim(t.Context(), goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if c := sessionCookieFrom(t, loginTo(t, f)); !c.Secure {
		t.Error("an https base URL did not imply a Secure cookie")
	}
}

func TestNeverIsHonouredEvenBehindAProxySayingHTTPS(t *testing.T) {
	f := newAccountFixture(t, "", withCookieSecure(config.CookieSecureNever))
	if err := f.account.Claim(t.Context(), goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	req, err := http.NewRequest("POST", f.srv.URL+"/api/login",
		strings.NewReader(`{"password":"`+goodPassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if c := sessionCookieFrom(t, resp); c.Secure {
		t.Error("never was overridden by the forwarded header")
	}
}

func TestTheForwardedHeaderStillWorksUnderAuto(t *testing.T) {
	f := newAccountFixture(t, "", withCookieSecure(config.CookieSecureAuto))
	if err := f.account.Claim(t.Context(), goodPassword); err != nil {
		t.Fatalf("claim: %v", err)
	}
	req, err := http.NewRequest("POST", f.srv.URL+"/api/login",
		strings.NewReader(`{"password":"`+goodPassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if c := sessionCookieFrom(t, resp); !c.Secure {
		t.Error("auto ignored a proxy saying https")
	}
}
