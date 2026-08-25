package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"

	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

var (
	encoderOnce sync.Once
	encoder     *zstd.Encoder
	decoderOnce sync.Once
	decoder     *zstd.Decoder
)

func zstdEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		// SpeedDefault over SpeedBestCompression: snapshots are written on
		// every event burst on hardware that may be a Raspberry Pi, and dedupe
		// already does most of the work of keeping the file small.
		encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	})
	return encoder
}

func zstdDecoder() *zstd.Decoder {
	decoderOnce.Do(func() {
		decoder, _ = zstd.NewReader(nil)
	})
	return decoder
}

// Hash returns the content address of b: the sha256 of the UNCOMPRESSED bytes.
// Hashing before compression keeps the address stable if the compression level
// or library ever changes.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PutBlob stores content and returns its hash. Identical content is stored
// once: this is what makes snapshotting 40 services every 5 minutes nearly
// free when nothing has changed.
func (s *Store) PutBlob(ctx context.Context, q *sqlcgen.Queries, content []byte) (string, error) {
	if q == nil {
		q = s.Q
	}
	hash := Hash(content)

	// Skip the compression work entirely when the blob is already present,
	// which is the common case by a wide margin.
	exists, err := q.BlobExists(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("check blob %s: %w", hash[:12], err)
	}
	if exists {
		return hash, nil
	}

	compressed := zstdEncoder().EncodeAll(content, nil)
	if err := q.PutBlob(ctx, sqlcgen.PutBlobParams{
		Hash:      hash,
		Size:      int64(len(content)),
		Content:   compressed,
		CreatedAt: Now(),
	}); err != nil {
		return "", fmt.Errorf("store blob %s: %w", hash[:12], err)
	}
	return hash, nil
}

// GetBlob returns the decompressed content for hash.
func (s *Store) GetBlob(ctx context.Context, hash string) ([]byte, error) {
	compressed, err := s.RQ.GetBlob(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", hash, err)
	}
	content, err := zstdDecoder().DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress blob %s: %w", hash, err)
	}
	return content, nil
}
