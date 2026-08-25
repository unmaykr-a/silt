package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/unmaykr-a/silt/internal/compose"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// redactionKeySetting names the per-install HMAC key in the settings table.
const redactionKeySetting = "redaction_hmac_key"

// RedactionKey returns the install's HMAC key, generating it on first call.
//
// It never leaves the database and is never logged. Without it the stored
// digests are not reversible; with a bare unkeyed hash they would be, for any
// low-entropy value.
func (s *Store) RedactionKey(ctx context.Context) ([]byte, error) {
	encoded, err := s.Q.GetSetting(ctx, redactionKeySetting)
	if err == nil {
		key, decErr := base64.StdEncoding.DecodeString(encoded)
		if decErr != nil {
			return nil, fmt.Errorf("decode redaction key: %w", decErr)
		}
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read redaction key: %w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate redaction key: %w", err)
	}
	if err := s.Q.PutSetting(ctx, sqlcgen.PutSettingParams{
		Key:   redactionKeySetting,
		Value: base64.StdEncoding.EncodeToString(key),
	}); err != nil {
		return nil, fmt.Errorf("store redaction key: %w", err)
	}
	return key, nil
}

// SnapshotResult reports what a write produced.
type SnapshotResult struct {
	ID             int64
	TakenAt        int64
	ConfigChanged  bool
	RuntimeChanged bool
	// Touched is true when the observation matched the previous snapshot
	// exactly and updated it in place instead of inserting a new one.
	Touched bool
}

// WriteSnapshot persists one observation of a project.
//
// The whole write is one transaction: a snapshot with half its service rows
// would misreport a change forever after.
func (s *Store) WriteSnapshot(
	ctx context.Context,
	projectID int64,
	takenAt int64,
	trigger string,
	obs compose.Observation,
) (SnapshotResult, error) {
	var result SnapshotResult

	err := s.Tx(ctx, func(q *sqlcgen.Queries) error {
		composeJSON, err := compose.CanonicalJSON(obs.Project)
		if err != nil {
			return fmt.Errorf("marshal project: %w", err)
		}
		composeHash, err := s.PutBlob(ctx, q, composeJSON)
		if err != nil {
			return err
		}

		// Inspect blobs must be stored before the fingerprint is computed,
		// since their hashes feed into it.
		runtimes := make([]compose.ServiceRuntime, len(obs.Runtimes))
		copy(runtimes, obs.Runtimes)
		for i := range runtimes {
			blob, ok := obs.InspectBlobs[runtimes[i].Service]
			if !ok {
				continue
			}
			hash, err := s.PutBlob(ctx, q, blob)
			if err != nil {
				return err
			}
			runtimes[i].InspectHash = hash
		}

		configFP := compose.ConfigFingerprint(composeHash, runtimes)
		runtimeFP := compose.RuntimeFingerprint(runtimes)

		configChanged, runtimeChanged := true, true
		prev, err := q.LatestSnapshot(ctx, projectID)
		switch {
		case err == nil:
			configChanged = prev.ConfigFingerprint != configFP
			runtimeChanged = prev.RuntimeFingerprint != runtimeFP
			// Keep taken_at strictly increasing per project. Two triggers can
			// land in the same millisecond — an event batch and a reconnect
			// reconcile, say — and UNIQUE (project_id, taken_at) would turn
			// that into a hard failure that silently loses the observation.
			if takenAt <= prev.TakenAt {
				takenAt = prev.TakenAt + 1
			}
		case errors.Is(err, sql.ErrNoRows):
			// First observation of this project: everything is new.
		default:
			return fmt.Errorf("read previous snapshot: %w", err)
		}
		result.ConfigChanged = configChanged
		result.RuntimeChanged = runtimeChanged
		result.TakenAt = takenAt

		// Nothing changed at all: record that the existing snapshot is still
		// current and stop. Inserting a row here would write a service_states
		// row per service and an env_keys row per variable — hundreds of rows
		// to say that nothing happened, which is what pushed an idle hour of
		// 40 services to hundreds of kilobytes.
		if !configChanged && !runtimeChanged {
			if err := q.TouchSnapshot(ctx, sqlcgen.TouchSnapshotParams{
				LastObservedAt: takenAt,
				ID:             prev.ID,
			}); err != nil {
				return fmt.Errorf("touch snapshot: %w", err)
			}
			result.ID = prev.ID
			result.TakenAt = prev.TakenAt
			result.Touched = true
			return nil
		}

		snap, err := q.InsertSnapshot(ctx, sqlcgen.InsertSnapshotParams{
			ProjectID:          projectID,
			TakenAt:            takenAt,
			Trigger:            trigger,
			ComposeHash:        composeHash,
			ComposeSource:      obs.Project.Source,
			ConfigFingerprint:  configFP,
			RuntimeFingerprint: runtimeFP,
			ConfigChanged:      boolToInt(configChanged),
			RuntimeChanged:     boolToInt(runtimeChanged),
			LastObservedAt:     takenAt,
		})
		if err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}
		result.ID = snap.ID

		for _, rt := range runtimes {
			if err := q.InsertServiceState(ctx, sqlcgen.InsertServiceStateParams{
				SnapshotID:     snap.ID,
				Service:        rt.Service,
				ContainerID:    rt.ContainerID,
				ContainerName:  rt.ContainerName,
				ImageRef:       rt.ImageRef,
				ImageID:        rt.ImageID,
				ImageDigest:    rt.ImageDigest,
				ImageCreatedAt: nullableMillis(rt.ImageCreated),
				State:          rt.State,
				Health:         rt.Health,
				RestartCount:   int64(rt.RestartCount),
				StartedAt:      rt.StartedAt,
				InspectHash:    nullableString(rt.InspectHash),
			}); err != nil {
				return fmt.Errorf("insert service state %s: %w", rt.Service, err)
			}

			// env_keys are content-addressed by the inspect blob, so this is a
			// no-op whenever the service's configuration is unchanged.
			for _, v := range rt.EnvKeys {
				var cleartext sql.NullString
				if !v.Redacted {
					cleartext = sql.NullString{String: v.Cleartext, Valid: true}
				}
				if err := q.InsertEnvKey(ctx, sqlcgen.InsertEnvKeyParams{
					InspectHash:    rt.InspectHash,
					Key:            v.Key,
					ValueHmac:      v.Sum,
					ValueLenBucket: v.Bucket,
					Redacted:       boolToInt(v.Redacted),
					Value:          cleartext,
				}); err != nil {
					return fmt.Errorf("insert env key %s/%s: %w", rt.Service, v.Key, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return SnapshotResult{}, err
	}
	return result, nil
}

// UpsertHostAndProject records a host and one of its projects.
func (s *Store) UpsertHostAndProject(ctx context.Context, hostName, endpoint, dockerVersion string, p projectIdentity) (int64, int64, error) {
	now := Now()
	host, err := s.Q.UpsertHost(ctx, sqlcgen.UpsertHostParams{
		Name:          hostName,
		Endpoint:      endpoint,
		DockerVersion: nullableString(dockerVersion),
		LastSeenAt:    sql.NullInt64{Int64: now, Valid: true},
		CreatedAt:     now,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert host: %w", err)
	}

	files, err := json.Marshal(p.ConfigFiles())
	if err != nil {
		return 0, 0, fmt.Errorf("marshal config files: %w", err)
	}
	project, err := s.Q.UpsertProject(ctx, sqlcgen.UpsertProjectParams{
		HostID:      host.ID,
		Name:        p.ProjectName(),
		WorkingDir:  p.ProjectWorkingDir(),
		ConfigFiles: string(files),
		FirstSeenAt: now,
		LastSeenAt:  now,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert project: %w", err)
	}
	return host.ID, project.ID, nil
}

// projectIdentity is the small surface UpsertHostAndProject needs, so the
// store does not depend on the docker package.
type projectIdentity interface {
	ProjectName() string
	ProjectWorkingDir() string
	ConfigFiles() []string
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullableMillis(ms int64) *int64 {
	if ms == 0 {
		return nil
	}
	return &ms
}
