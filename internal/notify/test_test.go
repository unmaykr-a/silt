package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/notify"
)

func TestTestIgnoresBlankTargets(t *testing.T) {
	got := notify.Test(context.Background(), []string{"", "   "})
	if len(got) != 0 {
		t.Errorf("blank targets produced %d results", len(got))
	}
}

func TestTestReportsAnUnparseableTarget(t *testing.T) {
	got := notify.Test(context.Background(), []string{"not-a-url"})
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1", len(got))
	}
	if got[0].OK {
		t.Error("an unparseable target reported success")
	}
	if got[0].Error == "" {
		t.Error("a failure with no reason is not worth showing")
	}
}

// The index is what lets the UI point at the row that failed, since two
// different targets can mask to the same string.
func TestTestNumbersItsResults(t *testing.T) {
	got := notify.Test(context.Background(), []string{"bad-one", "bad-two"})
	if len(got) != 2 {
		t.Fatalf("results = %d, want 2", len(got))
	}
	for i, r := range got {
		if r.Index != i {
			t.Errorf("result %d has index %d", i, r.Index)
		}
	}
}

// The point of the whole feature is defeated if telling someone their webhook
// is broken hands the webhook's token to whoever reads the message.
func TestTestNeverEchoesTheTarget(t *testing.T) {
	secrets := []struct {
		url    string
		tokens []string
	}{
		{"discord://tokenSECRET123@webhookIDSECRET", []string{"tokenSECRET123", "webhookIDSECRET"}},
		{"slack://hookSECRETaaa/hookSECRETbbb/hookSECRETccc", []string{"hookSECRETaaa", "hookSECRETccc"}},
		{"gotify://gotify.example.com:443/AppTokenSECRET", []string{"AppTokenSECRET"}},
		{"telegram://tokenSECRET@telegram?chats=123", []string{"tokenSECRET"}},
	}

	for _, tc := range secrets {
		results := notify.Test(context.Background(), []string{tc.url})
		if len(results) != 1 {
			t.Fatalf("%s: results = %d", tc.url, len(results))
		}
		rendered := results[0].Target + " " + results[0].Error
		for _, token := range tc.tokens {
			if strings.Contains(rendered, token) {
				t.Errorf("%s: rendered output leaks %q: %s", tc.url, token, rendered)
			}
		}
		if strings.Contains(rendered, tc.url) {
			t.Errorf("%s: rendered output contains the whole URL: %s", tc.url, rendered)
		}
	}
}

// A cancelled request must not be reported as a working target.
func TestTestHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, r := range notify.Test(ctx, []string{"gotify://gotify.invalid/token"}) {
		if r.OK {
			t.Error("a cancelled test reported success")
		}
	}
}

// The 40-character limit that suits a notification body cuts "failed to send
// discord notification: response status 401" down to "…: res", which is not an
// answer to anything.
func TestTestErrorsAreLongEnoughToRead(t *testing.T) {
	got := notify.Test(context.Background(), []string{"discord://tokenSECRET@1234"})
	if len(got) != 1 {
		t.Fatalf("results = %d", len(got))
	}
	msg := got[0].Error
	if len([]rune(msg)) < 45 && strings.HasSuffix(msg, "…") {
		t.Errorf("error truncated to something unreadable: %q", msg)
	}
	if strings.Contains(msg, "�") {
		t.Errorf("truncation split a rune: %q", msg)
	}
}
