package api

import (
	"sync"
	"time"
)

// A per-source rate limit for the ingest webhook.
//
// The token is the authentication and this is the blast radius when it leaks.
// A webhook token ends up in an Uptime Kuma config, a cron script and a Home
// Assistant automation — three places it can be read from — and without a
// limit one copy of it is unbounded writes into the timeline, which is both a
// disk-filling problem and a way to bury the changes the timeline exists to
// show.
//
// A fixed window rather than a token bucket: the question is "is something
// hammering this", not "smooth this traffic out", and a window someone can
// read off a clock is easier to explain in a log line than a refill rate.

// rateLimiter allows n events per window from each source.
type rateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	counts map[string]*rateWindow
	lastGC time.Time
	// Now is swappable for tests.
	Now func() time.Time
}

type rateWindow struct {
	start time.Time
	count int
}

func newRateLimiter(window time.Duration) *rateLimiter {
	return &rateLimiter{window: window, counts: map[string]*rateWindow{}, Now: time.Now}
}

func (l *rateLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// allow reports whether this source may send another event, and how long until
// its window resets when it may not.
//
// A limit of zero or less means unlimited, which is how the setting is turned
// off without a second flag to forget.
func (l *rateLimiter) allow(source string, limit int) (bool, time.Duration) {
	if limit <= 0 {
		return true, 0
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Sweep on the way past rather than on a timer. Sources are proxies and
	// monitors, so the map is small, and a goroutine per limiter is a lot of
	// machinery for a map nobody is watching grow.
	if now.Sub(l.lastGC) > l.window {
		for key, w := range l.counts {
			if now.Sub(w.start) > l.window {
				delete(l.counts, key)
			}
		}
		l.lastGC = now
	}

	w, ok := l.counts[source]
	if !ok || now.Sub(w.start) >= l.window {
		l.counts[source] = &rateWindow{start: now, count: 1}
		return true, 0
	}
	if w.count >= limit {
		return false, l.window - now.Sub(w.start)
	}
	w.count++
	return true, 0
}
