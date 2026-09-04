package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/unmaykr-a/silt/internal/store"
)

// The audit trail has its own retention window, and it was the one dial in the
// policy nothing exercised. It is also the one whose whole value is how far
// back it reaches, so pruning it on the wrong schedule is the failure that
// looks like nothing at all until someone asks who changed something.

func TestTheAuditTrailHasItsOwnWindow(t *testing.T) {
	db, _ := openTestStore(t)
	ctx := context.Background()

	for _, action := range []string{store.AuditSignIn, store.AuditSettingsChanged, store.AuditPrune} {
		if err := db.RecordAudit(ctx, store.AuditRecord{
			Actor: "someone", Action: action, Method: store.AuditByLocal,
		}); err != nil {
			t.Fatalf("record %s: %v", action, err)
		}
	}
	count, err := db.CountAudit(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("recorded %d entries, want 3", count)
	}

	// Events pruned to nothing, audit kept: the two windows are separate, and
	// inheriting the event tier would throw away the answer to "who changed
	// this" after a fortnight to save a few kilobytes.
	future := time.Now().Add(48 * time.Hour)
	stats, err := db.Prune(ctx, store.RetentionPolicy{Events: time.Hour}, future)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if stats.Audit != 0 {
		t.Errorf("pruned %d audit rows with no audit window set", stats.Audit)
	}
	if n, _ := db.CountAudit(ctx); n != 3 {
		t.Errorf("%d audit entries remain, want 3", n)
	}

	stats, err = db.Prune(ctx, store.RetentionPolicy{Audit: time.Hour}, future)
	if err != nil {
		t.Fatalf("prune with an audit window: %v", err)
	}
	if stats.Audit != 3 {
		t.Errorf("pruned %d audit rows, want 3", stats.Audit)
	}
	if n, _ := db.CountAudit(ctx); n != 0 {
		t.Errorf("%d audit entries remain after pruning, want 0", n)
	}
}

func TestKeepingTheAuditTrailForeverKeepsIt(t *testing.T) {
	// Zero is the documented "keep forever", and it has to survive a pass with
	// a now far in the future.
	db, _ := openTestStore(t)
	ctx := context.Background()
	if err := db.RecordAudit(ctx, store.AuditRecord{Actor: "someone", Action: store.AuditSignIn}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := db.Prune(ctx, store.RetentionPolicy{Audit: 0}, time.Now().Add(10*365*24*time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n, _ := db.CountAudit(ctx); n != 1 {
		t.Errorf("%d entries remain, want the one that is kept forever", n)
	}
}
