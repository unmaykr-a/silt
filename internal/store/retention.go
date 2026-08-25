package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// RetentionPolicy controls what is kept and for how long.
type RetentionPolicy struct {
	// Changed keeps snapshots with config_changed = 1.
	Changed time.Duration
	// Unchanged keeps snapshots with config_changed = 0. These are
	// proof-of-liveness and prune aggressively.
	Unchanged time.Duration
	// Events keeps rows in the events table. Event volume exceeds snapshot
	// volume by orders of magnitude, so it gets its own dial rather than
	// inheriting the long snapshot tier.
	Events time.Duration
}

// PruneStats reports one retention pass.
type PruneStats struct {
	UnchangedSnapshots int64
	ChangedSnapshots   int64
	Events             int64
	Blobs              int64
}

// Prune applies the retention policy and garbage-collects orphaned blobs.
//
// The whole pass runs in one transaction so a crash cannot leave snapshots
// referencing blobs that were already collected.
func (s *Store) Prune(ctx context.Context, policy RetentionPolicy, now time.Time) (PruneStats, error) {
	var stats PruneStats

	err := s.Tx(ctx, func(q *sqlcgen.Queries) error {
		if policy.Unchanged > 0 {
			n, err := q.PruneUnchangedSnapshots(ctx, now.Add(-policy.Unchanged).UnixMilli())
			if err != nil {
				return fmt.Errorf("prune unchanged snapshots: %w", err)
			}
			stats.UnchangedSnapshots = n
		}
		if policy.Changed > 0 {
			n, err := q.PruneChangedSnapshots(ctx, now.Add(-policy.Changed).UnixMilli())
			if err != nil {
				return fmt.Errorf("prune changed snapshots: %w", err)
			}
			stats.ChangedSnapshots = n
		}
		if policy.Events > 0 {
			n, err := q.PruneEvents(ctx, now.Add(-policy.Events).UnixMilli())
			if err != nil {
				return fmt.Errorf("prune events: %w", err)
			}
			stats.Events = n
		}

		// GC must run after the deletes and inside the same transaction, and
		// it walks service_states.inspect_hash as well as
		// snapshots.compose_hash. Inspect blobs are the majority of rows;
		// omitting them here would leak the entire store.
		if _, err := q.DeleteUnreferencedEnvKeys(ctx); err != nil {
			return fmt.Errorf("collect unreferenced env keys: %w", err)
		}
		n, err := q.DeleteUnreferencedBlobs(ctx)
		if err != nil {
			return fmt.Errorf("collect unreferenced blobs: %w", err)
		}
		stats.Blobs = n
		return nil
	})
	if err != nil {
		return PruneStats{}, err
	}
	return stats, nil
}

// Usage reports blob storage totals, for the size budget in M2's done
// criterion and for the settings screen later.
type Usage struct {
	Blobs             int64
	UncompressedBytes int64
	StoredBytes       int64
}

// Usage returns current blob accounting.
func (s *Store) Usage(ctx context.Context) (Usage, error) {
	row, err := s.RQ.CountBlobs(ctx)
	if err != nil {
		return Usage{}, fmt.Errorf("count blobs: %w", err)
	}
	return Usage{
		Blobs:             row.BlobCount,
		UncompressedBytes: row.UncompressedBytes,
		StoredBytes:       row.StoredBytes,
	}, nil
}

// Retainer runs Prune on a schedule.
type Retainer struct {
	Store    *Store
	Policy   RetentionPolicy
	Interval time.Duration
	// Vacuum reclaims free pages. Zero disables it; it rewrites the whole
	// file, so it belongs on a much longer cadence than pruning.
	Vacuum time.Duration
	Log    *slog.Logger
}

// Run blocks until ctx is cancelled.
func (r *Retainer) Run(ctx context.Context) error {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	interval := r.Interval
	if interval <= 0 {
		interval = time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastVacuum time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		stats, err := r.Store.Prune(ctx, r.Policy, time.Now())
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Error("retention pass failed", "error", err)
			continue
		}
		if stats.UnchangedSnapshots+stats.ChangedSnapshots+stats.Events+stats.Blobs > 0 {
			log.Info("pruned",
				"unchanged_snapshots", stats.UnchangedSnapshots,
				"changed_snapshots", stats.ChangedSnapshots,
				"events", stats.Events,
				"blobs", stats.Blobs,
			)
		}

		if r.Vacuum > 0 && time.Since(lastVacuum) >= r.Vacuum {
			if err := r.Store.Vacuum(ctx); err != nil {
				log.Error("vacuum failed", "error", err)
			} else {
				log.Info("vacuumed")
			}
			lastVacuum = time.Now()
		}
	}
}
