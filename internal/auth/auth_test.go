package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/unmaykr-a/silt/internal/auth"
)

func TestSafeNextRefusesAnythingOffSite(t *testing.T) {
	// An open redirect on a login endpoint is the classic way to make a
	// phishing link look like it came from the real site.
	for _, hostile := range []string{
		"https://evil.example/",
		"//evil.example/",
		"/\\evil.example",
		"http://evil.example",
		"javascript:alert(1)",
		"",
		"   ",
		"evil.example/path",
	} {
		if got := auth.SafeNext(hostile); got != "/" {
			t.Errorf("SafeNext(%q) = %q, want /", hostile, got)
		}
	}
}

func TestSafeNextKeepsALocalPath(t *testing.T) {
	for in, want := range map[string]string{
		"/":                          "/",
		"/projects/12":               "/projects/12",
		"/diff?from=1&to=2":          "/diff?from=1&to=2",
		"/projects/12/files?path=/x": "/projects/12/files?path=/x",
	} {
		if got := auth.SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProxyRefusesTheHeaderFromAnUntrustedPeer(t *testing.T) {
	// The whole security of forward auth: the header is settable by anyone who
	// can open a socket, so it means nothing unless the peer is the proxy.
	p, err := auth.NewProxy(true, "X-Remote-User", []string{"10.0.0.0/24", "192.168.1.5"})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	cases := []struct {
		remote string
		want   bool
	}{
		{"10.0.0.7:5000", true},
		{"192.168.1.5:41234", true},
		{"192.168.1.6:41234", false},
		{"10.0.1.7:5000", false},
		{"172.17.0.9:5000", false},
		{"not-an-address", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Remote-User", "andri")
		_, ok := p.Identify(r)
		if ok != tc.want {
			t.Errorf("peer %s accepted = %v, want %v", tc.remote, ok, tc.want)
		}
	}
}

func TestProxyWithNoTrustListSaysSo(t *testing.T) {
	p, err := auth.NewProxy(true, "X-Remote-User", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	if !p.TrustsAnySource() {
		t.Error("an empty trust list should report that it trusts any source, so startup can warn")
	}
	// It still works, because some deployments genuinely have nothing else on
	// the network — it is opt-in and warned about, not removed.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.Header.Set("X-Remote-User", "andri")
	if _, ok := p.Identify(r); !ok {
		t.Error("with no trust list the header should still be accepted")
	}
}

func TestProxyRejectsAMalformedTrustList(t *testing.T) {
	if _, err := auth.NewProxy(true, "", []string{"not-an-address"}); err == nil {
		t.Error("NewProxy accepted a trust list entry that is neither an IP nor a CIDR")
	}
}

// X-Forwarded-For must not decide who is trusted: believing a header in order
// to decide whether to believe a header is circular.
func TestProxyIgnoresForwardedForWhenDecidingTrust(t *testing.T) {
	p, _ := auth.NewProxy(true, "X-Remote-User", []string{"10.0.0.0/24"})
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.Header.Set("X-Forwarded-For", "10.0.0.7")
	r.Header.Set("X-Remote-User", "andri")
	if _, ok := p.Identify(r); ok {
		t.Error("an untrusted peer claimed a trusted address through X-Forwarded-For and was believed")
	}
}

func TestCrossSiteAllowsSafeMethodsAndSameOrigin(t *testing.T) {
	same := func(method string, headers map[string]string) *http.Request {
		r := httptest.NewRequest(method, "http://silt.lan/api/settings", nil)
		r.Host = "silt.lan"
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	if auth.CrossSite(same("GET", map[string]string{"Origin": "https://evil.example"}), nil) {
		t.Error("a GET was treated as cross-site; reads carry no state change")
	}
	if auth.CrossSite(same("PUT", map[string]string{"Sec-Fetch-Site": "same-origin"}), nil) {
		t.Error("Sec-Fetch-Site: same-origin was refused")
	}
	if auth.CrossSite(same("PUT", map[string]string{"Origin": "http://silt.lan"}), nil) {
		t.Error("a matching Origin was refused")
	}
	// A curl or a script carries no Origin and no ambient cookie, so there is
	// no confused deputy to exploit.
	if auth.CrossSite(same("POST", nil), nil) {
		t.Error("a request with no browser headers was refused; the API is meant to be scriptable")
	}
}

func TestCrossSiteRefusesAnotherOrigin(t *testing.T) {
	hostile := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "http://silt.lan/api/settings", nil)
		r.Host = "silt.lan"
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	if !auth.CrossSite(hostile(map[string]string{"Origin": "https://evil.example"}), nil) {
		t.Error("a POST from another origin was allowed")
	}
	if !auth.CrossSite(hostile(map[string]string{"Sec-Fetch-Site": "cross-site"}), nil) {
		t.Error("Sec-Fetch-Site: cross-site was allowed")
	}
	// Same site, different origin — http against https, or a neighbour on
	// another port — is what SameSite=Lax alone does not cover.
	if !auth.CrossSite(hostile(map[string]string{"Sec-Fetch-Site": "same-site"}), nil) {
		t.Error("Sec-Fetch-Site: same-site was allowed")
	}
	// A configured base URL is an origin the browser may legitimately use.
	if auth.CrossSite(hostile(map[string]string{"Origin": "https://silt.example.lan"}),
		[]string{"https://silt.example.lan"}) {
		t.Error("the configured base URL was refused as an origin")
	}
}

func TestSecureRequestFollowsTLSAndTheProxyHeader(t *testing.T) {
	plain := httptest.NewRequest("GET", "http://silt.lan/", nil)
	if auth.SecureRequest(plain) {
		t.Error("a plain HTTP request was reported as secure")
	}
	forwarded := httptest.NewRequest("GET", "http://silt.lan/", nil)
	forwarded.Header.Set("X-Forwarded-Proto", "https")
	if !auth.SecureRequest(forwarded) {
		t.Error("X-Forwarded-Proto: https was not believed")
	}
	// Proxies chain, and the client's own protocol is the first entry.
	chained := httptest.NewRequest("GET", "http://silt.lan/", nil)
	chained.Header.Set("X-Forwarded-Proto", "https, http")
	if !auth.SecureRequest(chained) {
		t.Error("a chained X-Forwarded-Proto was not read")
	}
}

func TestSecurityHeadersAreSet(t *testing.T) {
	handler := auth.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	// script-src without 'unsafe-inline' is the half that matters: it is what
	// stops an injected <script> from running at all.
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !contains(csp, want) {
			t.Errorf("CSP is missing %q: %s", want, csp)
		}
	}
	if contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP allows inline scripts: %s", csp)
	}
}

func TestPasswordThrottleLetsTheOwnerTypoAndThenSlowsDown(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	p, err := auth.NewPassword(string(hash))
	if err != nil {
		t.Fatalf("NewPassword: %v", err)
	}
	now := time.Now()
	p.Now = func() time.Time { return now }

	// Typing it wrong a couple of times must not lock anyone out.
	for i := 0; i < 3; i++ {
		if p.Verify("10.0.0.1", "wrong") {
			t.Fatal("the wrong password was accepted")
		}
		if blocked, _ := p.Throttled("10.0.0.1"); blocked {
			t.Fatalf("blocked after %d failures; the first few must be free", i+1)
		}
	}
	if !p.Verify("10.0.0.1", "right") {
		t.Fatal("the right password was refused within the free attempts")
	}

	// A success clears the record, so the count starts over.
	for i := 0; i < 4; i++ {
		p.Verify("10.0.0.1", "wrong")
	}
	blocked, wait := p.Throttled("10.0.0.1")
	if !blocked || wait <= 0 {
		t.Fatal("a run of failures did not produce a delay")
	}

	// The block is real: the correct password is refused while it stands.
	if p.Verify("10.0.0.1", "right") {
		t.Error("a throttled client was let in with the correct password")
	}
	// And it lifts on its own rather than needing an unlock.
	now = now.Add(wait + time.Second)
	if blocked, _ := p.Throttled("10.0.0.1"); blocked {
		t.Error("the block did not lift")
	}
	if !p.Verify("10.0.0.1", "right") {
		t.Error("the correct password was refused after the block lifted")
	}
}

func TestPasswordThrottleIsPerClient(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	p, _ := auth.NewPassword(string(hash))
	for i := 0; i < 6; i++ {
		p.Verify("10.0.0.1", "wrong")
	}
	if blocked, _ := p.Throttled("10.0.0.1"); !blocked {
		t.Fatal("the failing client is not blocked")
	}
	if blocked, _ := p.Throttled("10.0.0.2"); blocked {
		t.Error("one client's failures blocked another")
	}
}

// An attacker rotating source addresses must not be able to grow a map Silt
// never trims — that would be a memory leak reachable from outside.
func TestPasswordThrottleForgetsQuietClients(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	p, _ := auth.NewPassword(string(hash))
	now := time.Now()
	p.Now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		p.Verify(addr(i), "wrong")
	}
	if p.Tracked() < 50 {
		t.Fatalf("tracked %d clients, want the 50 that just failed", p.Tracked())
	}

	now = now.Add(time.Hour)
	p.Verify("10.9.9.9", "wrong")
	if p.Tracked() > 2 {
		t.Errorf("tracked %d clients an hour later; stale records are not being forgotten", p.Tracked())
	}
}

func TestDisabledPasswordNeverVerifies(t *testing.T) {
	p, err := auth.NewPassword("")
	if err != nil {
		t.Fatalf("NewPassword: %v", err)
	}
	if p.Enabled() {
		t.Error("an empty hash reports itself enabled")
	}
	if p.Verify("10.0.0.1", "") {
		t.Error("an unconfigured password accepted an empty string")
	}
}

func addr(i int) string { return "10.1." + itoa(i/256) + "." + itoa(i%256) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf []byte
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	return string(buf)
}

func contains(haystack, needle string) bool {
	return len(needle) <= len(haystack) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
