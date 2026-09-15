package api_test

import (
	"net/http"
	"testing"
)

// /metrics names every project on this host and counts its changes, which is
// why it is closed by default. Since 1.1.0 the toggle is a setting rather than
// an environment variable, so closing it has to work from the settings screen:
// this is the one endpoint whose exposure someone would want to change in a
// hurry, and "recreate the container" is not that.
//
// The chain under test is the whole of it — a signed-in administrator saves, and
// an anonymous request to /metrics is answered differently afterwards.
func TestMetricsExposureFollowsTheSetting(t *testing.T) {
	f := newAccountFixture(t, "")
	// Claiming the account is what makes the door shut: without it nothing is
	// authenticated and every endpoint answers, so the test would pass whatever
	// the setting said.
	if code, body := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatalf("claim account = %d %s", code, body)
	}

	anonymous := func() int {
		t.Helper()
		// A bare client with no cookie jar: the whole question is what someone
		// who has not signed in gets.
		code, _ := status(t, &http.Client{}, "GET", f.srv.URL+"/metrics", nil, "")
		return code
	}

	if code, body := f.do(t, "PUT", "/api/settings", `{"metrics_public":true}`); code != 200 {
		t.Fatalf("opening /metrics = %d %s", code, body)
	}
	if got := anonymous(); got != http.StatusOK {
		t.Errorf("anonymous /metrics with metrics_public true = %d, want 200", got)
	}

	if code, body := f.do(t, "PUT", "/api/settings", `{"metrics_public":false}`); code != 200 {
		t.Fatalf("closing /metrics = %d %s", code, body)
	}
	if got := anonymous(); got != http.StatusUnauthorized {
		t.Errorf("anonymous /metrics with metrics_public false = %d, want 401", got)
	}

	// And back, because the interesting direction for an operator is usually
	// re-opening it for a scrape that has started failing.
	if code, body := f.do(t, "PUT", "/api/settings", `{"reset":["metrics_public"]}`); code != 200 {
		t.Fatalf("resetting /metrics exposure = %d %s", code, body)
	}
	if got := anonymous(); got != http.StatusUnauthorized {
		t.Errorf("anonymous /metrics after reset = %d, want 401: the environment default is closed", got)
	}
}
