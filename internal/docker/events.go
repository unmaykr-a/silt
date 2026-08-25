package docker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

// noiseActions are event actions Silt never wants to see.
//
// Every container with a HEALTHCHECK emits exec_create and exec_start on every
// probe: 40 services on 30-second healthchecks is roughly 230,000 events a day,
// all of it noise. The rest are equally uninteresting API chatter.
//
// These are matched by PREFIX, not equality. Docker appends the command to
// exec actions ("exec_create: /bin/sh -c ..."), so an equality check silently
// matches nothing and the filter does no work at all.
var noiseActions = []string{
	"exec_create",
	"exec_start",
	"exec_die",
	"exec_detach",
	"top",
	"attach",
	"detach",
	"resize",
	"copy",
	"archive-path",
	"extract-to-dir",
	"export",
}

// isNoise reports whether an action should be discarded before it reaches the
// rest of Silt.
func isNoise(action string) bool {
	for _, n := range noiseActions {
		if action == n || strings.HasPrefix(action, n+":") || strings.HasPrefix(action, n+" ") {
			return true
		}
	}
	return false
}

// Backoff controls reconnection pacing.
type Backoff struct {
	Min time.Duration
	Max time.Duration
}

// DefaultBackoff is 1s doubling to a 30s ceiling.
var DefaultBackoff = Backoff{Min: time.Second, Max: 30 * time.Second}

// delay returns the wait before attempt n (0-indexed): a jittered duration in
// [Min, Min<<n], capped at Max.
//
// Jitter keeps a fleet of Silt instances from stampeding a proxy as it comes
// back. The Min floor matters just as much: unfloored full jitter can draw a
// near-zero wait, which turns a restarting proxy into a burst of retries and a
// wall of warnings before the ladder has climbed anywhere.
func (b Backoff) delay(attempt int) time.Duration {
	min, max := b.Min, b.Max
	if min <= 0 {
		min = time.Second
	}
	if max < min {
		max = min
	}
	d := min << attempt
	// Guard the shift overflowing into a negative duration on long outages.
	if d <= 0 || d > max {
		d = max
	}
	if d <= min {
		return min
	}
	return min + time.Duration(rand.Int64N(int64(d-min)))
}

// Watcher streams Docker events, reconnecting for as long as its context
// lives.
//
// The reconnect contract matters more than the happy path. The stream drops on
// proxy restarts, daemon upgrades and network blips; without recovery Silt
// silently stops recording and nobody notices until the outage it was meant to
// explain. See PROJECT.md Section 5.
type Watcher struct {
	Client  *Client
	Log     *slog.Logger
	Backoff Backoff

	// OnEvent receives every event that survives filtering.
	OnEvent func(Event)
	// OnConnect fires after each successful connection, including the first.
	// resumedFrom is zero on the first connection and otherwise carries the
	// timestamp the stream was resumed from. Callers reconcile here: event
	// replay is best-effort and the daemon may have dropped the gap.
	OnConnect func(resumedFrom time.Time)
	// OnDisconnect fires when an established stream drops.
	OnDisconnect func(err error)
}

// Run streams events until ctx is cancelled. It returns only ctx.Err().
func (w *Watcher) Run(ctx context.Context) error {
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	backoff := w.Backoff
	if backoff.Min == 0 && backoff.Max == 0 {
		backoff = DefaultBackoff
	}

	var (
		attempt int
		// last is the timestamp of the most recent event seen, used to resume
		// the stream across a reconnect.
		last time.Time
	)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		started := time.Now()
		connected, sawEvent, err := w.stream(ctx, &last)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		elapsed := time.Since(started)

		if connected {
			if w.OnDisconnect != nil {
				w.OnDisconnect(err)
			}
			log.Warn("docker event stream disconnected", "error", err, "connected_for", elapsed)
			// Only reset the ladder for a connection that did real work.
			// A stream that connects and immediately fails would otherwise
			// retry at full speed forever.
			if sawEvent || elapsed >= backoff.Max {
				attempt = 0
			}
		} else {
			log.Warn("docker engine unreachable", "error", err, "attempt", attempt+1)
		}

		d := backoff.delay(attempt)
		attempt++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

// stream runs one connection to /events. It reports whether the engine was
// reachable at all, so Run can tell a dropped stream from an engine that never
// answered, and whether any event arrived, so Run knows the connection did
// real work.
func (w *Watcher) stream(ctx context.Context, last *time.Time) (connected, sawEvent bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	opts := events.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", string(events.ContainerEventType)),
			filters.Arg("type", string(events.ImageEventType)),
		),
	}
	resumedFrom := *last
	if !resumedFrom.IsZero() {
		// Docker accepts a unix timestamp with optional nanoseconds. Resume one
		// nanosecond past the last event so it is not delivered twice.
		resume := resumedFrom.Add(time.Nanosecond)
		opts.Since = strconv.FormatInt(resume.Unix(), 10) + "." + strconv.FormatInt(int64(resume.Nanosecond()), 10)
	}

	// Probe the engine before subscribing. The Docker client hands back
	// channels immediately and never signals that the HTTP connection
	// succeeded, so without this a reconnect to a host that happens to be
	// quiet would never fire OnConnect — and the reconcile that covers the
	// replay gap would never run. That is precisely the case the reconnect
	// contract exists for.
	if _, err := w.Client.Version(ctx); err != nil {
		return false, false, err
	}
	connected = true
	if w.OnConnect != nil {
		w.OnConnect(resumedFrom)
	}

	msgs, errs := w.Client.api.Events(ctx, opts)

	for {
		select {
		case <-ctx.Done():
			return connected, sawEvent, ctx.Err()

		case err := <-errs:
			if err == nil || errors.Is(err, io.EOF) {
				return connected, sawEvent, io.EOF
			}
			return connected, sawEvent, err

		case msg, ok := <-msgs:
			if !ok {
				return connected, sawEvent, io.EOF
			}
			sawEvent = true
			at := eventTime(msg)
			if at.After(*last) {
				*last = at
			}
			if isNoise(string(msg.Action)) {
				continue
			}
			if w.OnEvent != nil {
				w.OnEvent(toEvent(msg, at))
			}
		}
	}
}

func eventTime(msg events.Message) time.Time {
	if msg.TimeNano != 0 {
		return time.Unix(0, msg.TimeNano)
	}
	if msg.Time != 0 {
		return time.Unix(msg.Time, 0)
	}
	return time.Now()
}

func toEvent(msg events.Message, at time.Time) Event {
	attrs := msg.Actor.Attributes
	return Event{
		Type:    string(msg.Type),
		Action:  string(msg.Action),
		Project: attrs[LabelProject],
		Service: attrs[LabelService],
		ActorID: msg.Actor.ID,
		Image:   attrs["image"],
		At:      at,
	}
}
