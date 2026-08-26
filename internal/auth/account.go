package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// Account is Silt's built-in administrator.
//
// It exists from first boot rather than being something you opt into. The
// previous default — open unless SILT_PASSWORD_HASH was set — meant the safe
// configuration was the one you had to know to ask for, and the unsafe one was
// what you got by following the quick start. Now the first person to reach the
// UI is asked to choose a password, and nothing else is reachable until they
// do.
//
// The window between the container starting and someone claiming it is real,
// and is the same window every first-run setup has. It is narrowed rather than
// closed: the API is refused throughout, the fact is logged loudly at startup,
// and anyone who would rather not have the window at all can set
// SILT_PASSWORD_HASH, which claims the account before it ever starts.
type Account struct {
	db *store.Store

	// envHash, when set, is the password and the UI cannot change it.
	// Declarative configuration wins: someone managing Silt from a compose
	// file should not find the UI has quietly diverged from it.
	envHash string
	// allowed is false when the local account is turned off entirely, for an
	// install that authenticates only through a provider.
	allowed bool

	// verifier carries the bcrypt comparison and the per-client throttle.
	verifier *Password

	mu    sync.RWMutex
	state accountState
	// Now is swappable for tests.
	Now func() time.Time
}

type accountState struct {
	hash        string
	enabled     bool
	oidcSubject string
}

// MinPasswordLength is what the setup form asks for.
//
// Ten rather than eight: this is the single credential in front of a read of
// every environment key and compose file on the host, and it is typed once
// into a password manager rather than repeatedly by hand.
const MinPasswordLength = 10

// LoadAccount reads the account, creating the row on first boot.
func LoadAccount(ctx context.Context, db *store.Store, envHash string, allowed bool) (*Account, error) {
	envHash = strings.TrimSpace(envHash)
	verifier, err := NewPassword(envHash)
	if err != nil {
		return nil, err
	}
	a := &Account{db: db, envHash: envHash, allowed: allowed, verifier: verifier, Now: time.Now}

	now := time.Now().UnixMilli()
	if err := db.Q.CreateLocalAccount(ctx, sqlcgen.CreateLocalAccountParams{
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("create local account: %w", err)
	}
	row, err := db.RQ.GetLocalAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("read local account: %w", err)
	}
	a.state = accountState{
		hash:        row.PasswordHash,
		enabled:     row.Enabled != 0,
		oidcSubject: row.OidcSubject,
	}
	a.syncVerifier()
	return a, nil
}

// syncVerifier points the bcrypt comparison at whichever hash is in force.
// Callers hold no lock; this takes what it needs.
func (a *Account) syncVerifier() {
	hash := a.effectiveHash()
	// NewPassword only fails on a malformed hash, and both sources are
	// validated before they get here — the environment at startup, the stored
	// one because Silt generated it.
	if next, err := NewPassword(hash); err == nil {
		next.Now = a.verifier.Now
		a.verifier = next
	}
}

func (a *Account) effectiveHash() string {
	if a.envHash != "" {
		return a.envHash
	}
	return a.state.hash
}

func (a *Account) snapshot() (accountState, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state, a.effectiveHash()
}

// Available reports whether the local account is in play at all.
func (a *Account) Available() bool { return a != nil && a.allowed }

// SetupRequired reports that the account exists but has no password yet, so
// the UI should ask for one before anything else.
func (a *Account) SetupRequired() bool {
	if !a.Available() {
		return false
	}
	state, hash := a.snapshot()
	return state.enabled && hash == ""
}

// Enabled reports whether a password sign-in is currently possible.
func (a *Account) Enabled() bool {
	if !a.Available() {
		return false
	}
	state, hash := a.snapshot()
	return state.enabled && hash != ""
}

// Active reports whether the account is switched on, claimed or not. This is
// what decides whether Silt refuses anonymous requests: an unclaimed account
// must lock the door, or the setup screen would be decoration.
func (a *Account) Active() bool {
	if !a.Available() {
		return false
	}
	state, _ := a.snapshot()
	return state.enabled
}

// ManagedByEnvironment reports that the password comes from
// SILT_PASSWORD_HASH, so the UI must not offer to change it.
func (a *Account) ManagedByEnvironment() bool { return a != nil && a.envHash != "" }

// LinkedSubject is the provider identity this account answers to, if any.
func (a *Account) LinkedSubject() string {
	if a == nil {
		return ""
	}
	state, _ := a.snapshot()
	return state.oidcSubject
}

// LinkedTo reports whether a provider subject is this account.
func (a *Account) LinkedTo(subject string) bool {
	if a == nil || subject == "" || !a.Available() {
		return false
	}
	state, _ := a.snapshot()
	return state.enabled && state.oidcSubject == subject
}

// Throttled reports whether a client must wait before another attempt.
func (a *Account) Throttled(client string) (bool, time.Duration) {
	if a == nil {
		return false, 0
	}
	a.mu.RLock()
	verifier := a.verifier
	a.mu.RUnlock()
	return verifier.Throttled(client)
}

// Verify checks a password against whichever hash is in force.
func (a *Account) Verify(client, password string) bool {
	if !a.Enabled() {
		return false
	}
	a.mu.RLock()
	verifier := a.verifier
	a.mu.RUnlock()
	return verifier.Verify(client, password)
}

// ErrWeakPassword explains a refused password in a way the form can show.
type ErrWeakPassword struct{ Reason string }

func (e *ErrWeakPassword) Error() string { return e.Reason }

// ErrNotClaimable means the account already has a password, or cannot take one.
var ErrNotClaimable = errors.New("the account has already been set up")

// Claim sets the first password. It is the only way in on a fresh install, and
// it works exactly once.
func (a *Account) Claim(ctx context.Context, password string) error {
	if !a.Available() {
		return ErrNotClaimable
	}
	if a.ManagedByEnvironment() {
		return ErrNotClaimable
	}
	if !a.SetupRequired() {
		return ErrNotClaimable
	}
	return a.setPassword(ctx, password)
}

// ChangePassword replaces the password, checking the current one first.
//
// The current password is required even though the caller is already signed
// in: a session someone walked away from should not be enough to lock its
// owner out.
func (a *Account) ChangePassword(ctx context.Context, client, current, next string) error {
	if !a.Enabled() {
		return errors.New("password sign-in is not enabled for this account")
	}
	if a.ManagedByEnvironment() {
		return errors.New("the password comes from SILT_PASSWORD_HASH; change it there")
	}
	if !a.Verify(client, current) {
		return errors.New("the current password is not correct")
	}
	return a.setPassword(ctx, next)
}

func (a *Account) setPassword(ctx context.Context, password string) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := a.db.Q.SetLocalPassword(ctx, sqlcgen.SetLocalPasswordParams{
		PasswordHash: string(hash),
		UpdatedAt:    a.now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("store password: %w", err)
	}

	a.mu.Lock()
	a.state.hash = string(hash)
	a.syncVerifier()
	a.mu.Unlock()
	return nil
}

// CheckPassword is the policy, in one place so the API and the account agree.
//
// Length and nothing else. Composition rules push people towards
// "Password1!" and a passphrase that fails them is the one worth encouraging.
func CheckPassword(password string) error {
	if len([]rune(password)) < MinPasswordLength {
		return &ErrWeakPassword{
			Reason: fmt.Sprintf("the password must be at least %d characters", MinPasswordLength),
		}
	}
	if strings.TrimSpace(password) == "" {
		return &ErrWeakPassword{Reason: "the password must not be only whitespace"}
	}
	return nil
}

// SetEnabled turns password sign-in on or off.
//
// Whether turning it off is safe is the caller's judgement: only the API layer
// knows whether a provider or a proxy would still let anyone in.
func (a *Account) SetEnabled(ctx context.Context, enabled bool) error {
	if !a.Available() {
		return errors.New("the local account is disabled by configuration")
	}
	value := int64(0)
	if enabled {
		value = 1
	}
	if err := a.db.Q.SetLocalEnabled(ctx, sqlcgen.SetLocalEnabledParams{
		Enabled:   value,
		UpdatedAt: a.now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("store account state: %w", err)
	}
	a.mu.Lock()
	a.state.enabled = enabled
	a.mu.Unlock()
	return nil
}

// Link records the provider subject that reaches this account. Passing an
// empty subject unlinks.
func (a *Account) Link(ctx context.Context, subject string) error {
	if !a.Available() {
		return errors.New("the local account is disabled by configuration")
	}
	subject = strings.TrimSpace(subject)
	if err := a.db.Q.SetLocalOIDCSubject(ctx, sqlcgen.SetLocalOIDCSubjectParams{
		OidcSubject: subject,
		UpdatedAt:   a.now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("store account link: %w", err)
	}
	a.mu.Lock()
	a.state.oidcSubject = subject
	a.mu.Unlock()
	return nil
}

func (a *Account) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
