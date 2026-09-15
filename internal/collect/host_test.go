package collect_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/unmaykr-a/silt/internal/collect"
)

// fakeHosts records what it was asked to rename and can be told that a name is
// already taken, which is the case the renamer has to decline rather than force.
type fakeHosts struct {
	mu    sync.Mutex
	calls [][2]string
	taken map[string]bool
	err   error
}

func (f *fakeHosts) RenameHost(_ context.Context, from, to string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2]string{from, to})
	if f.err != nil {
		return false, f.err
	}
	if f.taken[to] {
		return false, nil
	}
	return true, nil
}

func (f *fakeHosts) seen() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.calls...)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRenamingTheHostMovesItsHistory(t *testing.T) {
	hosts := &fakeHosts{}
	r := collect.NewHostRenamer(hosts, "local", quiet())

	if !r.Apply(t.Context(), "pi") {
		t.Fatal("a changed host name did not rename anything")
	}
	if got := hosts.seen(); len(got) != 1 || got[0] != [2]string{"local", "pi"} {
		t.Fatalf("renames = %v, want one local -> pi", got)
	}
}

// The setting is saved on every save, not only when it changed, so the
// unchanged case has to be free: a rename on every unrelated edit would be a
// write per save and a log line claiming something happened.
func TestSavingTheSameHostNameRenamesNothing(t *testing.T) {
	hosts := &fakeHosts{}
	r := collect.NewHostRenamer(hosts, "local", quiet())

	if r.Apply(t.Context(), "local") {
		t.Error("an unchanged host name reported a rename")
	}
	if got := hosts.seen(); len(got) != 0 {
		t.Errorf("renames = %v, want none", got)
	}
}

// Renaming twice must move from the name in force, not from the one the process
// booted with. Getting this wrong means the second rename looks for a row that
// no longer exists and silently does nothing.
func TestASecondRenameMovesFromTheCurrentName(t *testing.T) {
	hosts := &fakeHosts{}
	r := collect.NewHostRenamer(hosts, "local", quiet())

	r.Apply(t.Context(), "pi")
	r.Apply(t.Context(), "pi5")

	want := [][2]string{{"local", "pi"}, {"pi", "pi5"}}
	got := hosts.seen()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("renames = %v, want %v", got, want)
	}
}

// Renaming back to a label used before finds a row already holding it. Two
// histories stay two, and the caller is told nothing moved.
func TestRenamingOntoAnExistingHostDeclines(t *testing.T) {
	hosts := &fakeHosts{taken: map[string]bool{"old": true}}
	r := collect.NewHostRenamer(hosts, "local", quiet())

	if r.Apply(t.Context(), "old") {
		t.Error("renaming onto an existing host reported success")
	}
}

// A failed write must not leave the renamer believing it succeeded, or the next
// rename would move from a name nothing is stored under.
func TestAFailedRenameIsReportedNotSwallowed(t *testing.T) {
	hosts := &fakeHosts{err: errors.New("database is locked")}
	r := collect.NewHostRenamer(hosts, "local", quiet())

	if r.Apply(t.Context(), "pi") {
		t.Error("a failed rename reported success")
	}
}

// Two administrators saving at once must produce one rename, not two racing
// ones: the second would try to move a name the first already moved.
func TestConcurrentSavesRenameOnce(t *testing.T) {
	hosts := &fakeHosts{}
	r := collect.NewHostRenamer(hosts, "local", quiet())

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Apply(t.Context(), "pi")
		}()
	}
	wg.Wait()

	if got := hosts.seen(); len(got) != 1 {
		t.Fatalf("renames = %v, want exactly one", got)
	}
}
