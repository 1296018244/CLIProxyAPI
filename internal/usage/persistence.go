package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// SnapshotFilePayload is the on-disk representation for usage statistics snapshots.
type SnapshotFilePayload struct {
	Version    int                `json:"version"`
	ExportedAt time.Time          `json:"exported_at"`
	Usage      StatisticsSnapshot `json:"usage"`
}

// SaveSnapshotFile atomically writes the current usage statistics snapshot to path.
func SaveSnapshotFile(path string, stats *RequestStatistics) error {
	path = strings.TrimSpace(path)
	if path == "" || stats == nil {
		return nil
	}
	payload := SnapshotFilePayload{Version: 1, ExportedAt: time.Now().UTC(), Usage: stats.Snapshot()}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal usage snapshot: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create usage snapshot directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".usage-*.tmp")
	if err != nil {
		return fmt.Errorf("create usage snapshot temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if errRemove := os.Remove(tmpName); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Debug("failed to remove temporary usage snapshot file")
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		if errClose := tmp.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close usage snapshot temp file after write error")
		}
		return fmt.Errorf("write usage snapshot temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close usage snapshot temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace usage snapshot file: %w", err)
	}
	return nil
}

// LoadSnapshotFile loads an on-disk usage statistics snapshot into stats.
func LoadSnapshotFile(path string, stats *RequestStatistics) (MergeResult, error) {
	var zero MergeResult
	path = strings.TrimSpace(path)
	if path == "" || stats == nil {
		return zero, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return zero, nil
		}
		return zero, fmt.Errorf("read usage snapshot file: %w", err)
	}
	var payload SnapshotFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return zero, fmt.Errorf("parse usage snapshot file: %w", err)
	}
	if payload.Version != 0 && payload.Version != 1 {
		return zero, fmt.Errorf("unsupported usage snapshot version %d", payload.Version)
	}
	return stats.MergeSnapshot(payload.Usage), nil
}

// StartSnapshotPersistence periodically saves usage statistics and saves once on shutdown.
func StartSnapshotPersistence(ctx context.Context, path string, stats *RequestStatistics, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	path = strings.TrimSpace(path)
	if ctx == nil {
		ctx = context.Background()
	}
	if path == "" || stats == nil {
		close(done)
		return done
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				if err := SaveSnapshotFile(path, stats); err != nil {
					log.WithError(err).Warn("failed to save usage snapshot during shutdown")
				}
				return
			case <-ticker.C:
				if err := SaveSnapshotFile(path, stats); err != nil {
					log.WithError(err).Warn("failed to save usage snapshot")
				}
			}
		}
	}()
	return done
}
