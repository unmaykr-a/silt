package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/api"
	"github.com/unmaykr-a/silt/internal/auth"
	"github.com/unmaykr-a/silt/internal/config"
	"github.com/unmaykr-a/silt/internal/store"
)

// POST /api/auth/setup is public — it has to be reachable before anyone can
// sign in. Its own guard is therefore the only thing standing between a reader
// and the built-in administrator account, and "is anyone signed in?" is not
// enough once some of those people are readers.

// escalationServer is the reachable default: forward auth with an admin group,
// and a built-in account nobody has claimed yet.
func escalationServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	proxy, err := auth.NewProxy(true, "X-Remote-User", nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	proxy = proxy.WithAdminGroups("X-Remote-Groups", []string{"silt-admins"})

	// Available and unclaimed: SetupRequired is true, which is the state a
	// fresh install with a provider configured sits in until someone chooses
	// a password.
	account, err := auth.LoadAccount(ctx, db, "", true)
	if err != nil {
		t.Fatalf("LoadAccount: %v", err)
	}

	srv := api.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, nil, config.Config{}, nil)
	srv.SetAuth(&api.Gate{
		Sessions: auth.NewSessions(db, time.Hour, 0),
		Account:  account,
		Proxy:    proxy,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAViewerCannotClaimTheAdministratorAccount(t *testing.T) {
	ts := escalationServer(t)
	client := &http.Client{}

	code, body := status(t, client, http.MethodPost, ts.URL+"/api/auth/setup", map[string]string{
		"X-Remote-User":   "reader",
		"X-Remote-Groups": "users",
		"Content-Type":    "application/json",
		"Sec-Fetch-Site":  "same-origin",
	}, `{"password":"hunter2hunter2"}`)

	// Setting the password on the built-in account and being handed a session
	// for it is a straight promotion from reader to administrator, on the
	// default configuration of an install that has a provider and has never
	// claimed its local account.
	if code != http.StatusForbidden {
		t.Errorf("a viewer claimed the administrator account: %d %s", code, body)
	}
}

func TestAnAdministratorCanStillClaimTheAccount(t *testing.T) {
	ts := escalationServer(t)
	client := &http.Client{}

	code, body := status(t, client, http.MethodPost, ts.URL+"/api/auth/setup", map[string]string{
		"X-Remote-User":   "operator",
		"X-Remote-Groups": "users,silt-admins",
		"Content-Type":    "application/json",
		"Sec-Fetch-Site":  "same-origin",
	}, `{"password":"hunter2hunter2"}`)

	if code != http.StatusOK {
		t.Errorf("an administrator was refused: %d %s", code, body)
	}
}

func TestAnAnonymousStrangerCannotClaimItEither(t *testing.T) {
	// Already true, and worth keeping true: with a provider configured, an
	// anonymous claim would be a stranger taking the account rather than the
	// operator bootstrapping it.
	ts := escalationServer(t)
	client := &http.Client{}

	code, _ := status(t, client, http.MethodPost, ts.URL+"/api/auth/setup", map[string]string{
		"Content-Type":   "application/json",
		"Sec-Fetch-Site": "same-origin",
	}, `{"password":"hunter2hunter2"}`)

	if code == http.StatusOK {
		t.Error("an anonymous caller claimed the administrator account")
	}
}
