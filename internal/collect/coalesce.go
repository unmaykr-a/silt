// Package collect turns Docker activity into project-level observations.
package collect

import (
	"sync"
	"time"

	"github.com/unmaykr-a/silt/internal/docker"
)

// Batch is one project's worth of coalesced activity.
type Batch struct {
	Project string
	Events  []docker.Event
	First   time.Time
	Last    time.Time
}

// Actions returns the distinct actions in the batch, in first-seen order.
func (b Batch) Actions() []string {
	seen := make(map[string]bool, len(b.Events))
	out := make([]string, 0, len(b.Events))
	for _, e := range b.Events {
		if !seen[e.Action] {
			seen[e.Action] = true
			out = append(out, e.Action)
		}
	}
	return out
}

// Services returns the distinct non-empty service names in the batch, in
// first-seen order.
func (b Batch) Services() []string {
	seen := make(map[string]bool, len(b.Events))
	out := make([]string, 0, len(b.Events))
	for _, e := range b.Events {
		if e.Service == "" || seen[e.Service] {
			continue
		}
		seen[e.Service] = true
		out = append(out, e.Service)
	}
	return out
}

// Coalescer groups events per project into batches.
//
// One `docker compose up` fires a dozen container events in a second or two.
// Without coalescing, Silt would take twenty snapshots of one change. See
// PROJECT.md Section 5.
//
// The window opens on a project's first event and closes a fixed duration
// later, rather than resetting on every arrival: a resetting window can be held
// open indefinitely by a steady trickle of events, which is exactly what a
// crash-looping container produces.
type Coalescer struct {
	window time.Duration
	out    chan Batch

	mu      sync.Mutex
	pending map[string]*Batch
	done    chan struct{}
	closed  bool
	wg      sync.WaitGroup
}

// NewCoalescer returns a Coalescer with the given window.
func NewCoalescer(window time.Duration) *Coalescer {
	return &Coalescer{
		window:  window,
		out:     make(chan Batch),
		pending: make(map[string]*Batch),
		done:    make(chan struct{}),
	}
}

// C is the stream of completed batches.
func (c *Coalescer) C() <-chan Batch { return c.out }

// Add files an event into its project's batch, opening a window if needed.
// Events with no project label are dropped: they cannot be attributed, and
// host-level events are not what this stage is for.
func (c *Coalescer) Add(e docker.Event) {
	if e.Project == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}

	if b, ok := c.pending[e.Project]; ok {
		b.Events = append(b.Events, e)
		if e.At.After(b.Last) {
			b.Last = e.At
		}
		return
	}

	c.pending[e.Project] = &Batch{
		Project: e.Project,
		Events:  []docker.Event{e},
		First:   e.At,
		Last:    e.At,
	}

	c.wg.Add(1)
	go func(project string) {
		defer c.wg.Done()
		timer := time.NewTimer(c.window)
		defer timer.Stop()
		select {
		case <-timer.C:
			c.flush(project)
		case <-c.done:
			// Shutting down. The at-most-one-window of pending events is
			// dropped rather than forced onto a reader that has gone away.
		}
	}(e.Project)
}

func (c *Coalescer) flush(project string) {
	c.mu.Lock()
	b, ok := c.pending[project]
	if !ok {
		c.mu.Unlock()
		return
	}
	delete(c.pending, project)
	c.mu.Unlock()

	// Selecting on done matters: without it, a flush that is already past the
	// closed check and blocked on an unread channel would hang Close forever.
	select {
	case c.out <- *b:
	case <-c.done:
	}
}

// Close waits for in-flight windows to drain, then closes C. Add is a no-op
// afterwards.
func (c *Coalescer) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	c.mu.Unlock()

	c.wg.Wait()
	close(c.out)
}
