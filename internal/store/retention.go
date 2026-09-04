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
	// Audit keeps rows in audit_log.
	//
	// Its own dial, and by default a long one. The administrative trail is
	// tiny — a row per settings change, not per observation — and it is the
	// one table whose value is entirely in how far back it goes. Inheriting
	// the event tier would throw away the answer to "who changed this" after
	// a fortnight to save a few kilobytes.
	Audit time.Duration
}

// PruneStats reports one retention pass.
type PruneStats struct {
	UnchangedSnapshots int64
	ChangedSnapshots   int64
	Events             int64
	Audit              int64
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

		if policy.Audit > 0 {
			n, err := q.PruneAudit(ctx, now.Add(-policy.Audit).UnixMilli())
			if err != nil {
				return fmt.Errorf("prune audit log: %w", err)
			}
			stats.Audit = n
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

// RetentionSettings is everything the Retainer re-reads between passes.
type RetentionSettings struct {
	Policy   RetentionPolicy
	Interval time.Duration
	// Vacuum reclaims free pages. Zero disables it; it rewrites the whole
	// file, so it belongs on a much longer cadence than pruning.
	Vacuum time.Duration
}

// Retainer runs Prune on a schedule.
type Retainer struct {
	Store    *Store
	Policy   RetentionPolicy
	Interval time.Duration
	Vacuum   time.Duration
	Log      *slog.Logger

	// Live, when set, supersedes the static fields and is re-read after every
	// pass, so a retention window edited on the settings screen takes effect
	// on the next pass rather than on the next restart.
	Live func() RetentionSettings

	// Extra runs after each pass, for anything else on the same schedule.
	// Sessions use it: expiring them is the same "remove what is past its
	// window" job, and one timer is easier to reason about than two.
	Extra func(ctx context.Context)
}

// settings returns the policy in force right now.
func (r *Retainer) settings() RetentionSettings {
	if r.Live != nil {
		return r.Live()
	}
	return RetentionSettings{Policy: r.Policy, Interval: r.Interval, Vacuum: r.Vacuum}
}

// Run blocks until ctx is cancelled.
func (r *Retainer) Run(ctx context.Context) error {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	current := r.settings()
	interval := current.Interval
	if interval <= 0 {
		interval = time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Read from the database rather than starting at zero.
	//
	// The zero time is an infinitely long time ago, so a fresh Retainer would
	// vacuum on its first pass whatever the configured cadence — and since
	// this restarts with the container, a weekly vacuum on a host that pulls
	// images nightly was a nightly vacuum. VACUUM rewrites the entire file,
	// which is the one thing an SD card does not want done to it hourly.
	//
	// Starting at time.Now() instead has the opposite failure: a container
	// restarted more often than the interval would never vacuum at all.
	// Persisting it is the only version where the cadence means what it says.
	lastVacuum := r.lastVacuum(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		current = r.settings()
		if next := current.Interval; next > 0 && next != interval {
			interval = next
			ticker.Reset(interval)
		}

		stats, err := r.Store.Prune(ctx, current.Policy, time.Now())
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

		if r.Extra != nil {
			r.Extra(ctx)
		}

		if current.Vacuum > 0 && time.Since(lastVacuum) >= current.Vacuum {
			if err := r.Store.Vacuum(ctx); err != nil {
				log.Error("vacuum failed", "error", err)
			} else {
				log.Info("vacuumed")
			}
			// Recorded even when it failed, so a database that cannot be
			// vacuumed is retried on the next cadence rather than on every
			// single pass.
			lastVacuum = time.Now()
			r.recordVacuum(ctx, lastVacuum, log)
		}
	}
}

// lastVacuumSetting is where the time of the last VACUUM is kept, so the
// cadence survives a restart.
const lastVacuumSetting = "last_vacuum_at"

// lastVacuum is the zero time when nothing is recorded, which on a database
// that has never been vacuumed is the right answer: vacuum on the first pass.
func (r *Retainer) lastVacuum(ctx context.Context) time.Time {
	raw, err := r.Store.GetSetting(ctx, lastVacuumSetting)
	if err != nil {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return at
}

func (r *Retainer) recordVacuum(ctx context.Context, at time.Time, log *slog.Logger) {
	if err := r.Store.PutSetting(ctx, lastVacuumSetting, at.UTC().Format(time.RFC3339)); err != nil {
		// Not fatal: the worst case is one extra vacuum after a restart.
		log.Warn("could not record the vacuum time", "error", err)
	}
}
