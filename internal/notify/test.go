package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/containrrr/shoutrrr/pkg/types"
)

// Sending a test message, so a broken target is discovered on the settings
// screen rather than during the outage it was configured for.
//
// A shoutrrr URL is a string with a token in it and no feedback loop: it is
// wrong until something tries to send, and the only thing that tries to send is
// the change that mattered. Silt logs a failed notification, which helps
// whoever is reading logs at the time and nobody else.

// TestTimeout bounds one target. shoutrrr's senders take no context, so a
// target pointing at a host that black-holes packets would otherwise hold the
// request open until the browser gives up.
const TestTimeout = 10 * time.Second

// TestResult is one target's outcome.
//
// Target is masked, and Error is a string rather than an error because it is
// rendered: see sanitise for why it is not shoutrrr's message verbatim.
type TestResult struct {
	// Index is the target's position in the configured list, so the UI can
	// point at the row that failed even though every target is masked and two
	// may mask to the same text.
	Index  int
	Target string
	OK     bool
	Error  string
}

// Test sends one message to each configured target and reports each outcome.
//
// One sender per URL rather than one router over all of them: shoutrrr's
// router returns a slice of errors whose correspondence to the input list is
// not part of its documented contract, and the entire value of this feature is
// saying *which* target is broken.
func Test(ctx context.Context, urls []string) []TestResult {
	clean := make([]string, 0, len(urls))
	for _, u := range urls {
		if u = strings.TrimSpace(u); u != "" {
			clean = append(clean, u)
		}
	}

	out := make([]TestResult, 0, len(clean))
	for i, url := range clean {
		out = append(out, testOne(ctx, i, url))
	}
	return out
}

func testOne(ctx context.Context, index int, url string) TestResult {
	result := TestResult{Index: index, Target: Mask(url)}

	sender, err := New([]string{url}, Filter{}, nil)
	if err != nil {
		result.Error = sanitise(err.Error(), url)
		return result
	}
	if sender == nil {
		result.Error = "not a target"
		return result
	}

	// shoutrrr takes no context, so the send runs on its own goroutine and the
	// wait is bounded here. The channel is buffered: on timeout this function
	// returns while the send is still in flight, and an unbuffered channel
	// would leak the goroutine forever.
	done := make(chan []error, 1)
	go func() {
		params := types.Params{"title": "Silt: test notification"}
		done <- sender.router.Send(
			"This is a test from Silt. If you are reading it, this target works.",
			&params,
		)
	}()

	timeout := time.NewTimer(TestTimeout)
	defer timeout.Stop()

	select {
	case errs := <-done:
		for _, e := range errs {
			if e != nil {
				result.Error = sanitise(e.Error(), url)
				return result
			}
		}
		result.OK = true
		return result
	case <-timeout.C:
		result.Error = fmt.Sprintf("no response within %s", TestTimeout)
		return result
	case <-ctx.Done():
		result.Error = sanitise(errors.Join(ctx.Err()).Error(), url)
		return result
	}
}

// sanitise keeps a provider's error message useful without echoing the target.
//
// shoutrrr and the HTTP clients under it put pieces of the request URL into
// their error text — gotify quotes the app token back at you verbatim — and
// those pieces are the credential. An error rendered on the settings screen,
// copied into an issue, or pasted into a chat would carry the token with it,
// which is the failure this whole feature exists to prevent people discovering
// the hard way.
//
// So rather than guessing which providers quote what, every fragment of the URL
// is redacted out of the message: the whole string, and then each piece it
// splits into on the characters that separate a shoutrrr URL's parts. Some
// over-redaction is the intended trade — Target already carries the scheme and,
// where it is not itself a secret, the host.
func sanitise(msg, url string) string {
	msg = strings.ReplaceAll(msg, url, "…")

	_, rest, ok := strings.Cut(url, "://")
	if !ok {
		rest = url
	}
	for _, fragment := range strings.FieldsFunc(rest, isURLSeparator) {
		// Four characters is below any credential and above the fragments
		// that collide with ordinary words in an error message.
		if len(fragment) >= minSecretFragment {
			msg = strings.ReplaceAll(msg, fragment, "…")
		}
	}

	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "failed"
	}
	// A longer limit than a notification body's: this is read once, on the
	// settings screen, by someone trying to work out why a target is broken.
	// The 40 characters that suit a config value cut "failed to send discord
	// notification: response status 401" down to "…: res".
	return truncateTo(msg, 200)
}

// minSecretFragment is the shortest piece of a target URL treated as possibly
// secret. Below it a fragment is a port or a path word, not a token.
const minSecretFragment = 4

func isURLSeparator(r rune) bool {
	switch r {
	case '/', '@', ':', '?', '&', '=', '#', ',', ';':
		return true
	}
	return false
}
