package auth

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Password is the bcrypt fallback login, with a throttle in front of it.
//
// bcrypt is deliberately slow and compares in constant time, which handles the
// offline attack. It does nothing about an online one: a script can still work
// through a wordlist at whatever rate the CPU allows. The throttle turns that
// from "a few thousand guesses an hour" into "a handful", without ever locking
// the owner out permanently.
type Password struct {
	hash string

	mu       sync.Mutex
	failures map[string]*attempt
	// Now is swappable for tests.
	Now func() time.Time
}

type attempt struct {
	count int
	// until is when the block lifts. Zero means not blocked.
	until time.Time
	// seen is the last failure, used only to forget a quiet client. Kept
	// separate from until: one field doing both jobs meant the first wrong
	// password blocked the next fifteen minutes.
	seen time.Time
}

const (
	// Free attempts before any delay. Typing a password wrong twice is normal.
	freeAttempts = 3
	// The first penalty, doubling per failure to a ceiling.
	baseDelay = 2 * time.Second
	maxDelay  = 5 * time.Minute
	// How long a quiet client keeps its record. Long enough that waiting out
	// the lockout is slower than the lockout itself.
	forgetAfter = 15 * time.Minute
)

// NewPassword validates the hash up front, because a typo in
// SILT_PASSWORD_HASH would otherwise silently lock the owner out with no
// signal beyond "incorrect password" forever.
func NewPassword(hash string) (*Password, error) {
	hash = strings.TrimSpace(hash)
	p := &Password{hash: hash, failures: map[string]*attempt{}, Now: time.Now}
	if hash == "" {
		return p, nil
	}
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return nil, fmt.Errorf("SILT_PASSWORD_HASH is not a valid bcrypt hash: %w", err)
	}
	return p, nil
}

// Enabled reports whether a password is configured.
func (p *Password) Enabled() bool { return p != nil && p.hash != "" }

func (p *Password) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Throttled reports whether a client must wait, and for how long.
func (p *Password) Throttled(client string) (bool, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.failures[client]
	if !ok {
		return false, 0
	}
	now := p.now()
	if a.until.IsZero() || !now.Before(a.until) {
		return false, 0
	}
	return true, a.until.Sub(now)
}

// Verify checks a password and records the outcome against client.
//
// The client key is the caller's choice — an address, a proxy-asserted one —
// because Silt cannot know which of those is meaningful in someone else's
// network.
func (p *Password) Verify(client, password string) bool {
	if !p.Enabled() {
		return false
	}
	if blocked, _ := p.Throttled(client); blocked {
		return false
	}

	ok := bcrypt.CompareHashAndPassword([]byte(p.hash), []byte(password)) == nil

	p.mu.Lock()
	defer p.mu.Unlock()
	p.forgetStale()
	if ok {
		delete(p.failures, client)
		return true
	}

	a := p.failures[client]
	if a == nil {
		a = &attempt{}
		p.failures[client] = a
	}
	a.count++
	a.seen = p.now()
	if a.count > freeAttempts {
		delay := baseDelay << min(a.count-freeAttempts-1, 10)
		if delay > maxDelay || delay <= 0 {
			delay = maxDelay
		}
		a.until = a.seen.Add(delay)
	}
	return false
}

// forgetStale keeps the map from growing without bound. Callers hold the lock.
//
// Without this an attacker rotating source addresses would be writing entries
// Silt never removes, which is a memory leak reachable from outside.
func (p *Password) forgetStale() {
	cutoff := p.now().Add(-forgetAfter)
	for key, a := range p.failures {
		// Still serving a block is never stale, however long ago it started.
		if !a.until.IsZero() && a.until.After(p.now()) {
			continue
		}
		if a.seen.Before(cutoff) {
			delete(p.failures, key)
		}
	}
}

// Tracked reports how many clients currently have a failure record. Test-only
// visibility into the leak this is meant not to have.
func (p *Password) Tracked() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.failures)
}
