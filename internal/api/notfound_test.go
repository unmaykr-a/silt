package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// Anything under /api/ that no route claimed used to fall through to the SPA
// handler, so a POST to a GET-only endpoint answered 200 with a page of HTML.
// A caller then sees a success it cannot parse, and the actual mistake is
// several hours away.

func TestTheWrongMethodOnARealEndpointSaysSo(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("setup failed")
	}

	for _, c := range []struct {
		method, path, allow string
	}{
		{"POST", "/api/auth/login", "GET"},
		{"DELETE", "/api/auth", "GET"},
		{"POST", "/api/settings", "GET, PUT, DELETE"},
	} {
		code, body := f.do(t, c.method, c.path, "{}")
		if code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d %s, want 405", c.method, c.path, code, body)
			continue
		}
		if !strings.Contains(body, `"error"`) {
			t.Errorf("%s %s answered %s, want a JSON error", c.method, c.path, body)
		}
	}
}

func TestAnEndpointThatDoesNotExistIsA404(t *testing.T) {
	f := newAccountFixture(t, "")
	if code, _ := f.do(t, "POST", "/api/auth/setup", `{"password":"`+goodPassword+`"}`); code != 200 {
		t.Fatal("setup failed")
	}

	code, body := f.do(t, "GET", "/api/no-such-thing", "")
	if code != http.StatusNotFound {
		t.Errorf("GET /api/no-such-thing = %d %s, want 404", code, body)
	}
	if strings.Contains(body, "<!doctype") {
		t.Error("an unrouted API path was answered with the single-page app")
	}
}

func TestTheSinglePageAppStillOwnsEverythingElse(t *testing.T) {
	// The fallback must not have eaten the app's own routes: /projects/3 is a
	// client-side path and belongs to the web handler, not to a 404.
	//
	// What the web handler *answers* depends on whether this binary embeds a
	// built frontend, and the go job deliberately does not build one — a clean
	// checkout carries only dist/.gitkeep, so there it serves its own 503
	// explaining that. Asserting the served page would make this test a test
	// of the build. What it means to assert is which handler replied.
	f := newAccountFixture(t, "")
	for _, path := range []string{"/", "/projects/3", "/settings"} {
		code, body := f.do(t, "GET", path, "")
		switch {
		case code == http.StatusOK && strings.Contains(strings.ToLower(body), "<!doctype html"):
			// The app, from a binary that embeds it.
		case code == http.StatusServiceUnavailable && strings.Contains(body, "built without the web UI"):
			// The web handler saying there is no app embedded, which is still
			// the web handler.
		default:
			t.Errorf("GET %s = %d %.80s; the API fallback answered a client-side route", path, code, body)
		}
	}
}
