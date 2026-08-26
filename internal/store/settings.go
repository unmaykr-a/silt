package store

import (
	"context"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// GetSetting reads one row from the key/value settings table.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	return s.RQ.GetSetting(ctx, key)
}

// PutSetting writes one row, on the write pool.
func (s *Store) PutSetting(ctx context.Context, key, value string) error {
	return s.Q.PutSetting(ctx, sqlcgen.PutSettingParams{Key: key, Value: value})
}
