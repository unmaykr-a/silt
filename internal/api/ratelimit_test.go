package api_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// The ingest token lives in an Uptime Kuma config, a cron script and a Home
// Assistant automation — three places it can be read from. Without a limit one
// copy of it is unbounded writes into the timeline, which fills a disk and
// buries the changes the timeline exists to show.

func TestIngestRefusesAFloodFromOneSource(t *testing.T) {
	f := newFixture(t)
	auth := map[string]string{"Authorization": "Bearer " + f.ingestTok}

	limit := f.ingestLimit
	if limit <= 0 {
		t.Fatal("the fixture has no ingest limit configured")
	}

	for i := 0; i < limit; i++ {
		resp, body := f.post(t, "/api/ingest",
			fmt.Sprintf(`{"type":"probe.%d"}`, i), auth)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d = %d %s, want 202", i, resp.StatusCode, body)
		}
	}

	resp, body := f.post(t, "/api/ingest", `{"type":"probe.over"}`, auth)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the event past the limit = %d %s, want 429", resp.StatusCode, body)
	}
	// Retry-After tells a well-behaved sender when to come back rather than
	// leaving it to guess or spin.
	after := resp.Header.Get("Retry-After")
	if after == "" {
		t.Error("a 429 with no Retry-After")
	} else if n, err := strconv.Atoi(after); err != nil || n <= 0 || n > 61 {
		t.Errorf("Retry-After = %q, want seconds within the window", after)
	}
}

func TestTheLimitIsCheckedAfterTheToken(t *testing.T) {
	// Order matters: if an unauthenticated caller could use up the window,
	// the limiter would be a way to silence someone's monitoring rather than
	// a way to protect it.
	f := newFixture(t)
	bad := map[string]string{"Authorization": "Bearer wrong"}

	for i := 0; i < f.ingestLimit+10; i++ {
		resp, _ := f.post(t, "/api/ingest", `{"type":"probe"}`, bad)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated event %d = %d, want 401 every time", i, resp.StatusCode)
		}
	}

	// The real sender's window is untouched.
	resp, body := f.post(t, "/api/ingest", `{"type":"probe.real"}`,
		map[string]string{"Authorization": "Bearer " + f.ingestTok})
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("a real event after the flood = %d %s", resp.StatusCode, body)
	}
}
