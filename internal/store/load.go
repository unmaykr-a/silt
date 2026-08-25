package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// ServiceRuntimeState is the volatile state recorded for one service.
//
// Deliberately a store-local type: the diff package should not have to know
// about the database, and the database should not have to know about diffing.
type ServiceRuntimeState struct {
	State        string
	Health       string
	RestartCount int
	ImageRef     string
	ImageID      string
	ImageDigest  string
}

// SnapshotModel is a stored snapshot decoded back into a comparable form.
type SnapshotModel struct {
	Snapshot sqlcgen.Snapshot
	Project  compose.Project
	Runtimes map[string]ServiceRuntimeState
}

// LoadSnapshotModel reads a snapshot, its compose blob and its service states.
func (s *Store) LoadSnapshotModel(ctx context.Context, id int64) (SnapshotModel, error) {
	snap, err := s.RQ.GetSnapshot(ctx, id)
	if err != nil {
		return SnapshotModel{}, fmt.Errorf("read snapshot %d: %w", id, err)
	}

	raw, err := s.GetBlob(ctx, snap.ComposeHash)
	if err != nil {
		return SnapshotModel{}, err
	}
	var project compose.Project
	if err := json.Unmarshal(raw, &project); err != nil {
		return SnapshotModel{}, fmt.Errorf("decode compose blob for snapshot %d: %w", id, err)
	}

	states, err := s.RQ.ListServiceStates(ctx, id)
	if err != nil {
		return SnapshotModel{}, fmt.Errorf("read service states for snapshot %d: %w", id, err)
	}
	runtimes := make(map[string]ServiceRuntimeState, len(states))
	for _, st := range states {
		runtimes[st.Service] = ServiceRuntimeState{
			State:        st.State,
			Health:       st.Health,
			RestartCount: int(st.RestartCount),
			ImageRef:     st.ImageRef,
			ImageID:      st.ImageID,
			ImageDigest:  st.ImageDigest,
		}
	}

	return SnapshotModel{Snapshot: snap, Project: project, Runtimes: runtimes}, nil
}
