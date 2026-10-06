package collect_test

import (
	"bytes"
	"os"
	"testing"
)

// eventSentinel is planted in a container label. Labels arrive in the same
// attribute map as the engine's own facts about an event, so this is the string
// that must not follow an exit code into the database.
const eventSentinel = "SILT-EVENT-SENTINEL-c4ca4238a0b923820dcc509a6f75849b"

// TestAnEventNeverCarriesALabelToDisk.
//
// Capturing the engine's event attributes is what makes a 03:00 container.die
// answerable rather than merely true — it is where the exit code comes from.
// But the engine puts the container's labels in that same map, and secrets live
// in labels: a Traefik basicauth middleware keeps password hashes in one.
//
// internal/store has a sentinel test for snapshots, and it did not cover events
// at all, because until now an event's payload was three fields Silt wrote
// itself. The moment that payload started coming from the engine, it needed the
// same guarantee and the same kind of check: plant the string, run it through
// the real collector into a real database, then scan the file and its
// write-ahead log for the bytes.
func TestAnEventNeverCarriesALabelToDisk(t *testing.T) {
	h := newHarness(t)
	h.serve("media", "radarr", "radarr:5.4.0", "sha256:aaa", nil, nil)

	msg := containerEvent("die", "media", "radarr")
	// The fact worth keeping, beside three ways a secret reaches a label.
	msg.Actor.Attributes["exitCode"] = "137"
	msg.Actor.Attributes["traefik.http.middlewares.a.basicauth.users"] = "admin:" + eventSentinel
	msg.Actor.Attributes["com.example.api-token"] = eventSentinel
	msg.Actor.Attributes[eventSentinel] = "a secret in the key rather than the value"

	runFor(t, collector(h), func() {
		waitFor(t, "the watcher to subscribe", func() bool { return h.engine.Subscriptions() > 0 })
		h.engine.Emit(msg)
		waitFor(t, "the die event to be stored", func() bool {
			return hasEvent(t, h.db, "container.die")
		})
	})

	// The exit code did survive, or this test would pass by capturing nothing.
	var payload string
	for _, row := range storedEvents(t, h.db) {
		if row.Type == "container.die" {
			payload = row.Payload
		}
	}
	if payload == "" {
		t.Fatal("no payload on the die event")
	}
	if !bytes.Contains([]byte(payload), []byte(`"exit_code":"137"`)) {
		t.Errorf("the exit code was not captured: %s", payload)
	}
	if !bytes.Contains([]byte(payload), []byte(`"withheld_attributes":3`)) {
		t.Errorf("the withheld count is wrong or missing: %s", payload)
	}

	for _, path := range []string{h.dbPath, h.dbPath + "-wal"} {
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(raw, []byte(eventSentinel)) {
			t.Errorf("%s contains a container label from an event", path)
		}
	}
}
