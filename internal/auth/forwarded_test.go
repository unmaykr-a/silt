package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unmaykr-a/silt/internal/auth"
)

// The login throttle keys on whatever this returns, so a value an attacker can
// choose is a throttle an attacker can walk around, and a value shared by every
// client is a throttle that locks out the owner. Both failures are tested here
// because each is the other's fix taken too far.

func request(remote string, forwarded ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = remote
	for _, v := range forwarded {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func proxyWith(t *testing.T, trusted ...string) *auth.Proxy {
	t.Helper()
	// Forward auth off: the trust list decides what the throttle may believe
	// whether or not Silt is reading an identity header.
	p, err := auth.NewProxy(false, "X-Remote-User", trusted)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return p
}

func TestForwardedForTakesTheClientBehindATrustedProxy(t *testing.T) {
	p := proxyWith(t, "10.0.0.0/24")

	got, ok := p.ForwardedFor(request("10.0.0.7:5000", "203.0.113.9"))
	if !ok || got != "203.0.113.9" {
		t.Fatalf("ForwardedFor = %q, %v; want the forwarded client", got, ok)
	}
}

func TestForwardedForIgnoresWhatTheClientPrepended(t *testing.T) {
	p := proxyWith(t, "10.0.0.0/24")

	// What a browser sends when it sets its own X-Forwarded-For: the proxy
	// keeps it and appends the peer it actually saw. The left-most entry is
	// therefore the attacker's to choose, and reading it -- which is how this
	// is usually written -- would let one client mint a fresh throttle key per
	// request and guess passwords without limit.
	got, ok := p.ForwardedFor(request("10.0.0.7:5000", "198.51.100.1, 203.0.113.9"))
	if !ok {
		t.Fatal("ForwardedFor refused a request through a trusted proxy")
	}
	if got == "198.51.100.1" {
		t.Fatal("took the client-supplied entry; an attacker chooses their own throttle key")
	}
	if got != "203.0.113.9" {
		t.Fatalf("ForwardedFor = %q; want the address the trusted proxy added", got)
	}
}

func TestForwardedForSkipsTrustedHopsFromTheRight(t *testing.T) {
	p := proxyWith(t, "10.0.0.0/24", "172.16.0.0/16")

	// Two proxies in front: the client, then the outer proxy, then the inner
	// one. Both of the latter are trusted, so the answer is the client.
	got, ok := p.ForwardedFor(request("10.0.0.7:5000", "203.0.113.9, 172.16.4.4", "10.0.0.9"))
	if !ok || got != "203.0.113.9" {
		t.Fatalf("ForwardedFor = %q, %v; want the client beyond both proxies", got, ok)
	}
}

func TestForwardedForRefusesWithoutATrustList(t *testing.T) {
	// An empty list means "believe the identity header from any source", which
	// is a decision about a network. It is not a reason to believe a forwarded
	// address: with no list there is nothing to tell a proxy from a client.
	p := proxyWith(t)

	if got, ok := p.ForwardedFor(request("203.0.113.9:5000", "198.51.100.1")); ok {
		t.Fatalf("ForwardedFor = %q with no trust list; want the socket address to stand", got)
	}
}

func TestForwardedForRefusesAnUntrustedPeer(t *testing.T) {
	p := proxyWith(t, "10.0.0.0/24")

	if got, ok := p.ForwardedFor(request("203.0.113.9:5000", "198.51.100.1")); ok {
		t.Fatalf("ForwardedFor = %q from an untrusted peer; want refusal", got)
	}
}

func TestForwardedForRefusesWhenEveryHopIsAProxy(t *testing.T) {
	p := proxyWith(t, "10.0.0.0/24")

	// Nothing in the chain is a client, so there is no client address to key
	// on. Returning a proxy's would be the shared key this exists to avoid.
	if got, ok := p.ForwardedFor(request("10.0.0.7:5000", "10.0.0.9, 10.0.0.8")); ok {
		t.Fatalf("ForwardedFor = %q; want refusal when every hop is trusted", got)
	}
}

func TestForwardedForIgnoresGarbage(t *testing.T) {
	p := proxyWith(t, "10.0.0.0/24")

	if got, ok := p.ForwardedFor(request("10.0.0.7:5000", "not-an-address")); ok {
		t.Fatalf("ForwardedFor = %q for an unparseable entry; want refusal", got)
	}
	if got, ok := p.ForwardedFor(request("10.0.0.7:5000")); ok {
		t.Fatalf("ForwardedFor = %q with no header; want refusal", got)
	}
	// A real client address after an unparseable hop is still the answer.
	got, ok := p.ForwardedFor(request("10.0.0.7:5000", "203.0.113.9, junk"))
	if !ok || got != "203.0.113.9" {
		t.Fatalf("ForwardedFor = %q, %v; want the parseable client", got, ok)
	}
}

func TestForwardedForNilProxy(t *testing.T) {
	var p *auth.Proxy
	if _, ok := p.ForwardedFor(request("10.0.0.7:5000", "203.0.113.9")); ok {
		t.Fatal("a nil Proxy believed a forwarded address")
	}
}
